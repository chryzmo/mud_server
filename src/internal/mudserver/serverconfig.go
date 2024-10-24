package mudserver

import (
	"encoding/json"
	"os"
)

type Configuration struct {
	Server struct {
		ServerName	string `json:"ServerName"`
		ListenPort	string `json:"ListenPort"`
	} `json:"Server"`
	Database struct {
		DatabaseHost string `json:"DatabaseHost"`
		DatabasePort string `json:"DatabasePort"`
		DatabaseUsername string `json:"DatabaseUsername"`
		DatabasePassword string `json:"DatabasePassword"`
		DatabaseName string `json:"DatabaseName"`
	} `json:"Database"`
}

func (c Configuration) LoadConfig (config_file string) (Configuration) {
	pwd, _ := os.Getwd()
	file, _ := os.Open(pwd+"/"+config_file)
	defer file.Close()

	decoder := json.NewDecoder(file)
	serverconfig := Configuration{}

	err := decoder.Decode(&serverconfig)
	if err != nil {
		panic(err)
	}

	return serverconfig

}