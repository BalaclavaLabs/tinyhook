package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
)

type Config struct {
	Port      int               `json:"port"`
	Repo      string            `json:"repo"`
	Branch    string            `json:"branch"`
	Directory string            `json:"directory"`
	Events    []string          `json:"events"`
	Entry     []string          `json:"entry"`
	Env       map[string]string `json:"env"`
	Process   *os.Process
}

func (c Config) RepoUrl() *url.URL {
	u, err := url.Parse(c.Repo)
	if err != nil {
		log.Fatal("Invalid Repo URL")
	}
	return u
}

func (c Config) Init() {
	if c.Directory == "" {
		c.Directory = ".tinyhook"
	}

	info, err := os.Stat(c.Directory)
	if err != nil {
		os.Mkdir(c.Directory, os.ModePerm)
		info, _ = os.Stat(c.Directory)
	}
	if !info.IsDir() {
		os.Remove(c.Directory)
		os.Mkdir(c.Directory, os.ModePerm)
	}

	url := c.RepoUrl()

	loc := fmt.Sprintf("%s/%s", c.Directory, url.Path)

	cmd := exec.Command("git", "clone", c.Repo, loc)
	cmd.Run()

	cmd = exec.Command("git", "checkout", c.Branch)
	cmd.Dir = loc
	cmd.Run()

	cmd = exec.Command(c.Entry[0], c.Entry[1:]...)
	cmd.Env = c.BuildEnv()
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Start()

	c.Process = cmd.Process
}

func (c Config) RestartProcess() {
	log.Print("restarting process")
	if c.Process != nil {
		log.Print("Killing Process ", c.Process.Pid)
		err := c.Process.Kill()
		if err != nil {
			log.Fatal(err)
		}
	}

	url := c.RepoUrl()

	loc := fmt.Sprintf("%s/%s", c.Directory, url.Path)

	cmd := exec.Command("git", "pull")
	cmd.Dir = loc
	cmd.Stdout = os.Stdout
	cmd.Run()

	cmd = exec.Command(c.Entry[0], c.Entry[1:]...)
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Env = c.BuildEnv()
	cmd.Start()

	c.Process = cmd.Process
}

func (c Config) BuildEnv() []string {
	env := os.Environ()
	for key, value := range c.Env {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func ReadConfig() Config {
	c := Config{}
	j, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatal("No config found!")
	}
	err = json.Unmarshal(j, &c)
	if err != nil {
		log.Fatalf("error reading config: %v", err)
	}
	c.Init()
	return c
}

type HookHandler struct {
	config Config
}

func (h HookHandler) Events() []string {
	return h.config.Events
}

func (h HookHandler) Ref() string {
	return fmt.Sprintf("refs/heads/%s", h.config.Branch)
}

func (h HookHandler) HandlePush(p PushEvent) {
	log.Print(p.Ref, h.Ref())
	if p.Ref == h.Ref() {
		h.config.RestartProcess()
	}
}

func (h HookHandler) HandleEvent(ev string, e []byte) {
	if ev == "push" {
		p := PushEvent{}
		json.Unmarshal(e, &p)
		log.Print("push", p.Ref)
		h.HandlePush(p)
	}
}

type ErrorCallback *func(w http.ResponseWriter)

func (h HookHandler) ReadBody(r *http.Request) ([]byte, ErrorCallback) {
	body, err := io.ReadAll(r.Body)

	if err == nil {
		return body, nil
	}

	cb := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
	}

	return body, &cb
}

func (h HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if "push" == r.Header.Get("X-Github-Event") {
		b, cb := h.ReadBody(r)

		log.Print(string(b))

		if cb != nil {
			(*cb)(w)
			return
		}

		h.HandleEvent("push", b)
	}

	w.WriteHeader(200)
}

func main() {
	h := HookHandler{ReadConfig()}
	http.ListenAndServe(fmt.Sprintf(":%d", h.config.Port), h)
}
