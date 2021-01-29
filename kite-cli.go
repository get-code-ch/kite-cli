package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	kite "github.com/get-code-ch/kite-common"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type CLIConf struct {
	Name    string       `json:"name"`
	ApiKey  string       `json:"api_key"`
	Server  string       `json:"server"`
	Port    string       `json:"port"`
	Ssl     bool         `json:"ssl"`
	Address kite.Address `json:"address"`
}

type CLI struct {
	conf     *CLIConf
	conn     *websocket.Conn
	wg       sync.WaitGroup
	filename string
}

const defaultConfigFile = "./config/default.json"

func loadConfig(configFile string) *CLIConf {

	// New config creation
	c := new(CLIConf)

	// If no config file is provided we use "hardcoded" default filepath
	if configFile == "" {
		configFile = defaultConfigFile
	}

	// Testing if config file exist if not, return a fatal error
	_, err := os.Stat(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Panicf("Config file %s not exist\n", configFile)
		} else {
			log.Panicf("Something wrong with config file %s -> %v\n", configFile, err)
		}
	}

	// Reading and parsing configuration file
	if buffer, err := ioutil.ReadFile(configFile); err != nil {
		log.Printf(fmt.Sprintf("Error reading config file --> %v", err))
		return nil
	} else {
		if err := json.Unmarshal(buffer, c); err != nil {
			log.Panicf("Error parsing configuration file --> %v", err)
		}
		return c
	}
}

func (cli *CLI) sendSetup(setupFile string) {
	if _, err := os.Stat(setupFile); err != nil {
		if os.IsNotExist(err) {
			log.Printf("Setup folder \"%s\" not exist\n", setupFile)
		} else {
			log.Printf("Something wrong with setup folder %s -> %v\n", setupFile, err)
		}
	} else {
		setupRoot, _ := filepath.Abs(filepath.Dir(setupFile))
		_ = setupRoot
		// Reading and parsing configuration file
		if buffer, err := ioutil.ReadFile(setupFile); err != nil {
			log.Printf(fmt.Sprintf("Error reading config file --> %v", err))
		} else {
			setup := make(map[string]interface{})
			if err := json.Unmarshal(buffer, &setup); err != nil {
				log.Printf("Error parsing configuration file --> %v", err)
				return
			}
			// Check if all config files are present
			for key, value := range setup {
				if key != "description" && key != "api_key" {
					src := value.(map[string]interface{})["src"].(string)
					if _, err := os.Stat(fmt.Sprintf("%s\\%s", setupRoot, src)); err != nil {
						log.Printf("%s, source file %s\\%s is missig", key, setupRoot, src)
						return
					}
				}
			}

			// Sending setup files
			setupValid := true
			data := kite.SetupMessage{}

			for key, value := range setup {
				switch key {
				case "description":
					data.Description = value.(string)
					break
				case "api_key":
					data.ApiKey = value.(string)
					break
				default:
					file := kite.SetupFile{}
					src := value.(map[string]interface{})["src"].(string)
					dest := value.(map[string]interface{})["dest"].(string)
					file.Path = dest
					if content, err := ioutil.ReadFile(fmt.Sprintf("%s\\%s", setupRoot, src)); err == nil {
						file.Content = content
						data.SetupFiles = append(data.SetupFiles, file)
					} else {
						log.Printf("Something wrong with %s\\%s --> %s\n", setupRoot, src, err)
						setupValid = false
					}
				}

				if key != "description" {
				}
			}
			if setupValid {
				if err := cli.conn.WriteJSON(kite.Message{Action: kite.A_SETUP, Sender: cli.conf.Address, Data: data}); err != nil {
					log.Printf("Error sending configuration --> %v", err)
				}
			}

		}
	}
}

func (cli *CLI) waitMessage() {
	for {
		message := kite.Message{}
		//		if _, _, err := cli.conn.ReadMessage(); err != nil {
		if err := cli.conn.ReadJSON(&message); err != nil {
			log.Printf("Error on readMessage -> %v", err)
			cli.wg.Done()
			return
		} else {
			switch message.Action {
			case kite.A_LOG: //A_READLOG response
				fmt.Println()
				for _, lmi := range message.Data.([]interface{}) {
					lm := kite.LogMessage{}
					lm = lm.SetFromInterface(lmi)
					fmt.Printf("%s %s, %s\n", lm.Time.Local().Format("2006/01/02 15:04:05"), lm.Address, lm.Message)
				}

				fmt.Printf("%s> ", cli.conf.Address)
				break
			case kite.A_VALUE:
				fmt.Println()
				/*
					for key, value := range message.Data.(map[string]interface{}) {
						fmt.Printf("%s %v (%s)\n", key, value, reflect.TypeOf(value).String())
					}
				*/
				data := message.Data.(map[string]interface{})
				switch data["type"].(string) {
				case "gpio":
					fmt.Printf("Value for %s --> %t\n", data["description"].(string), data["value"].(bool))
					break
				case "float":
					fmt.Printf("Value for %s --> %.2f %s\n", data["description"].(string), data["value"].(float64), data["unit"].(string))
					break
				case "string":
					fmt.Printf("Value for %s --> %s %s\n", data["description"].(string), data["value"].(string), data["unit"].(string))
					break
				default:

				}
				// Printing prompt
				fmt.Printf("%s> ", cli.conf.Address)
				break
			case kite.A_EXPORT:
				if _, err := os.Stat(cli.filename); err != nil {
					if !os.IsNotExist(err) {
						cli.filename = ""
					}
				}

				if cli.filename == "" {
					log.Printf("Export error, missing or wrong filename (%s)\n", cli.filename)
					fmt.Printf("%s> ", cli.conf.Address)
					break
				}
				if content, err := json.Marshal(message.Data); err == nil {
					ioutil.WriteFile(cli.filename, content, os.ModePerm)
					cli.filename = ""
				}
				break
			default:
				fmt.Println()
				log.Printf("Message received (%s) ->  %v", message.Action, message.Data)
				fmt.Printf("%s> ", cli.conf.Address)
			}
		}
	}
}

func (cli *CLI) sendMessage(input chan []byte) {

	inputRe := regexp.MustCompile(`^([^:@]*)(?:@([^:]*))?(?::(.+))?$`)

	for {
		// Parsing input string
		if parsed := inputRe.FindSubmatch(<-input); parsed != nil {
			to := kite.Address{Domain: "*", Type: kite.H_ANY, Host: "*", Address: "*", Id: "*"}
			msg := ""

			action := kite.Action(strings.ToLower(string(parsed[1])))
			to.StringToAddress(string(parsed[2]))

			// No wildcard is authorized if no domain is selected endpoint domain is filled
			if to.Domain == "*" {
				to.Domain = cli.conf.Address.Domain
			}

			if err := action.IsValid(); err == nil {
				//log.Printf("Action --> %s", action)
				switch action {
				case kite.A_SETUP:
					cli.sendSetup(string(parsed[3]))
					break
				case kite.A_IMPORT:
					filename := ""
					if len(parsed) == 4 {
						filename = string(parsed[3])
					}

					if _, err := os.Stat(filename); err != nil || filename == "" {
					}

					if content, err := ioutil.ReadFile(filename); err != nil {
						log.Printf("Import error, somehting wrong with file %s --> %v \n", filename, err)
						break
					} else {
						msg = string(content)
					}

					message := kite.Message{Action: action, Sender: cli.conf.Address, Receiver: to, Data: msg}

					if err := cli.conn.WriteJSON(message); err != nil {
						cli.wg.Done()
						return
					}
					break
				case kite.A_EXPORT:
					if len(parsed) == 4 {
						cli.filename = string(parsed[3])
					}

					if _, err := os.Stat(cli.filename); err != nil {
						if !os.IsNotExist(err) {
							cli.filename = ""
						}
					}

					if cli.filename == "" {
						log.Printf("Export error, missing or wrong filename (%s)\n", cli.filename)
						//fmt.Printf("%s> ", cli.conf.Address)
						break
					}
					message := kite.Message{Action: action, Sender: cli.conf.Address, Receiver: to, Data: msg}

					if err := cli.conn.WriteJSON(message); err != nil {
						cli.wg.Done()
						return
					}
					break
				default:
					if len(parsed) == 4 {
						msg = string(parsed[3])
					}

					message := kite.Message{Action: action, Sender: cli.conf.Address, Receiver: to, Data: msg}

					if err := cli.conn.WriteJSON(message); err != nil {
						cli.wg.Done()
						return
					}
				}
			} else {
				log.Printf("%s", err)
				fmt.Printf("%s> ", cli.conf.Address)
			}
		} else {
			log.Printf("Invalid command ({action}[@{destination}]{:message})")
			fmt.Printf("%s> ", cli.conf.Address)
		}
	}
}

func (cli *CLI) readStdin(input chan []byte) {
	for {
		fmt.Printf("%s> ", cli.conf.Address)
		msg := bufio.NewScanner(os.Stdin)
		msg.Scan()
		if len(msg.Bytes()) == 0 {
			cli.wg.Done()
			return
		}
		input <- msg.Bytes()
	}
}

func main() {
	var err error
	var response *http.Response

	chanMsg := make(chan []byte)

	// Loading configuration
	configFile := ""
	if len(os.Args) >= 2 {
		configFile = os.Args[1]
	}
	cli := new(CLI)
	cli.filename = ""
	cli.conf = loadConfig(configFile)

	// Configure Server URL
	addr := flag.String("addr", fmt.Sprintf("%s:%s", cli.conf.Server, cli.conf.Port), "kite server http(s) address")
	flag.Parse()

	serverURL := url.URL{}
	if cli.conf.Ssl {
		serverURL = url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
	} else {
		serverURL = url.URL{Scheme: "ws", Host: *addr, Path: "/ws"}
	}

	// Adding origin in header, for server cross origin resource sharing (CORS) check
	header := http.Header{}
	header.Set("Origin", serverURL.String())

	// Connecting kite server, if connection failed retrying every x seconds
	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	//	if cli.conn, response, err = websocket.DefaultDialer.Dial(serverURL.String(), header); err != nil {
	if cli.conn, response, err = dialer.Dial(serverURL.String(), header); err != nil {
		count := 0
		log.Printf("Dial Error %v\n", err)
		cli.conn = nil

		for {
			time.Sleep(5 * time.Second)
			if cli.conn, response, err = dialer.Dial(serverURL.String(), header); err == nil {
				break
			} else {
				count++
				log.Printf("Dial Error %v (%d times)\n", err, count)
			}
		}
	}
	log.Printf("kite server connectected, (http status %d)", response.StatusCode)

	// Configuring ping handler (just logging a ping on stdin
	cli.conn.SetPingHandler(func(data string) error {
		return nil
	})

	// Connection is now established, now we sending cli registration to server
	message := kite.Message{Action: "register", Sender: cli.conf.Address, Data: cli.conf.ApiKey}
	if err := cli.conn.WriteJSON(message); err != nil {
		log.Printf("Error registring cli on sever --> %v", err)
	}

	// Reading server response
	if err = cli.conn.ReadJSON(&message); err != nil {
		log.Printf("Error registring cli on sever --> %v", err)
	} else {
		if message.Action == kite.A_ACCEPTED {
			log.Printf("\nConnection accepted from %s\n", message.Sender)
		} else {
			log.Printf("\nUnattended response from %s\n", message.Sender)
		}
	}

	cli.wg.Add(1)
	// Listening new server message
	go cli.waitMessage()

	// Reading prompt
	go cli.readStdin(chanMsg)

	// Sending message
	go cli.sendMessage(chanMsg)
	cli.wg.Wait()
}
