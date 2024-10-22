package mudserver

import (
	"encoding/json"
	"fmt"
	"os"
)

type Configuration struct {
	ServerName	string
	ListenPort	string
}

func (c Configuration) LoadConfig (config_file string) (Configuration) {
	pwd, _ := os.Getwd()
	file, _ := os.Open(pwd+"/"+config_file)
	fmt.Println(pwd+"/"+config_file)
	defer file.Close()

	decoder := json.NewDecoder(file)
	serverconfig := Configuration{}

	err := decoder.Decode(&serverconfig)
	if err != nil {
		panic(err)
	}

	fmt.Println("Server Config!")
	fmt.Println(serverconfig)

	return serverconfig

}