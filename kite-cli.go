package main

import (
	"bufio"
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
	"time"
)

type CLIConf struct {
	Name     string        `json:"name"`
	ApiKey   string        `json:"api_key"`
	Server   string        `json:"server"`
	Port     string        `json:"port"`
	Ssl      bool          `json:"ssl"`
	Endpoint kite.Endpoint `json:"endpoint"`
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
			log.Panic(fmt.Sprintf("Config file %s not exist\n", configFile))
		} else {
			log.Panic(fmt.Sprintf("Something wrong with config file %s -> %v\n", configFile, err))
		}
	}

	// Reading and parsing configuration file
	if buffer, err := ioutil.ReadFile(configFile); err != nil {
		log.Printf(fmt.Sprintf("Error reading config file --> %v", err))
		return nil
	} else {
		json.Unmarshal(buffer, c)
		return c
	}
}

func main() {
	var err error
	var response *http.Response
	var conn *websocket.Conn

	wait := make(chan bool)

	// Loading configuration
	configFile := ""
	if len(os.Args) >= 2 {
		configFile = os.Args[1]
	}
	conf := loadConfig(configFile)


	addr := flag.String("addr", fmt.Sprintf("%s:%s", conf.Server, conf.Port), "kite server http(s) address")
	flag.Parse()

	serverURL := url.URL{Scheme: "ws", Host: *addr, Path: "/ws"}

	// Adding origin in header, for server cross origin resource sharing (CORS) check
	header := http.Header{}
	header.Set("Origin", serverURL.String())

	// Connecting CommandCenter server
	if conn, response, err = websocket.DefaultDialer.Dial(serverURL.String(), header); err != nil {
		count := 0
		log.Printf("Dial Error %v\n", err)
		conn = nil

		for {
			time.Sleep(5 * time.Second)
			if conn, response, err = websocket.DefaultDialer.Dial(serverURL.String(), header); err == nil {
				break
			} else {
				count++
				log.Printf("Dial Error %v (%d times)\n", err, count)
			}
		}
	}
	//defer conn.Close()
	log.Printf("kite server connectected, (http status %d)", response.StatusCode)

	conn.SetPingHandler(func(data string) error {
		log.Println("Ping received")
		fmt.Print(">")
		return nil
	})

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				log.Printf("Error on readMessage -> %v", err)
				wait <- false
				return
			}
		}
	}()

	go func() {
		for {
			fmt.Print(">")
			msg := bufio.NewScanner(os.Stdin)
			msg.Scan()
			if len(msg.Bytes()) == 0 {
				wait <- false
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg.Bytes()); err != nil {
				wait <- false
				return
			}
		}
	}()

	for {
		select {
		case <-wait:
			log.Println("kite-cli exiting")
			return
		}
	}
}
