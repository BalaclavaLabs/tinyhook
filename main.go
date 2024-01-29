package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

type Config struct {
	Port int               `json:"port"`
	Repo string            `json:"repo"`
	Env  map[string]string `json:"env"`
}

func ReadConfig() Config {
	c := Config{}
	j, err := os.ReadFile("./")
	if err != nil {
		log.Fatal("No config found!")
	}
	err = json.Unmarshal(j, &c)
	if err != nil {
		log.Fatalf("error reading config: %v", err)
	}
	return c
}

type HookHandler struct {
	config Config
}

func (h HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	log.Print(string(b))
	w.WriteHeader(200)
}

func main() {
	h := HookHandler{ ReadConfig() }
	http.ListenAndServe(fmt.Sprintf(":%d", h.config.Port), h)
}
