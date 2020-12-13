set GOARCH=amd64
set GOOS=linux
go build  -o ./build/release/linux_amd64/kite-cli
set GOARCH=arm
set GOARM=5
set GOOS=linux
go build  -o ./build/release/linux_arm5/kite-cli
set GOARCH=amd64
set GOOS=windows
go build  -o ./build/release/windows_amd64/kite-cli.exe
