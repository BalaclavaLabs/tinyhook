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
	"strings"
)

type Config struct {
	Apps map[string]struct {
		Repo   string            `json:"repo"`
		Branch string            `json:"branch"`
		Events []string          `json:"events"`
		Build  []string          `json:"build"`
		Entry  []string          `json:"entry"`
		Env    map[string]string `json:"env"`
	} `json:"apps"`
	HTTPConfig map[string]struct {
		External int `json:"external"`
		Internal int `json:"internal"`
	} `json:"http_config"`
	UIPort       int    `json:"ui_port"`
	HookPort     int    `json:"hook_port"`
	Directory    string `json:"directory"`
	NginxVersion string `json:"nginx_version"`
	Processes    map[string]*os.Process
}

func (c Config) RepoUrl(name string) *url.URL {
	app := c.Apps[name]

	u, err := url.Parse(app.Repo)
	if err != nil {
		log.Fatal("Invalid Repo URL")
	}
	return u
}

func (c *Config) Init() {
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
	if (c.Processes == nil) {
		c.Processes = map[string]*os.Process{}
	}
	for name, _ := range c.Apps {
		c.StartProcess(name)
	}
}

func (c Config) PushProcess (name string, proc *os.Process) {
	c.Processes[name] = proc
}

func (c Config) StartProcess (name string) {
	app := c.Apps[name]
	url := c.RepoUrl(name)

	loc := fmt.Sprintf("%s/%s", c.Directory, url.Path)

	cmd := exec.Command("git", "clone", app.Repo, loc)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("now running git clone %s %s", app.Repo, loc)
	cmd.Run()

	cmd = exec.Command("git", "checkout", app.Branch)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = loc
	log.Printf("now running git checkout %s", app.Branch)
	cmd.Run()

	cmd = exec.Command("git", "pull")
	cmd.Dir = loc
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Print("now running git pull")
	cmd.Run()

	cmd = exec.Command(app.Build[0], app.Build[1:]...)
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("now running ", strings.Join(app.Build, " "))
	cmd.Run()

	cmd = exec.Command(app.Entry[0], app.Entry[1:]...)
	cmd.Env = c.BuildEnv(name)
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("now running ", strings.Join(app.Entry, " "))
	cmd.Start()

	c.PushProcess(name, cmd.Process)
}

func (c Config) RestartProcess(name string) {
	log.Print("restarting process")
	proc := c.Processes[name]
	if proc != nil {
		log.Print("Killing Process ", proc.Pid)
		cmd := exec.Command("kill", fmt.Sprintf("%d", proc.Pid))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}

	url := c.RepoUrl(name)
	app := c.Apps[name]

	loc := fmt.Sprintf("%s/%s", c.Directory, url.Path)

	cmd := exec.Command("git", "checkout", app.Branch)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Dir = loc
	log.Printf("now running git checkout %s", app.Branch)
	cmd.Run()

	cmd = exec.Command("git", "pull")
	cmd.Dir = loc
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Print("now running git pull")
	cmd.Run()

	cmd = exec.Command(app.Build[0], app.Build[1:]...)
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("now running ", strings.Join(app.Build, " "))
	cmd.Run()

	cmd = exec.Command(app.Entry[0], app.Entry[1:]...)
	cmd.Dir = loc
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Env = c.BuildEnv(name)
	log.Printf("now running ", strings.Join(app.Entry, " "))
	cmd.Start()

	c.PushProcess(name, proc)
}

func (c Config) BuildEnv(name string) []string {
	env := os.Environ()
	app := c.Apps[name]
	for key, value := range app.Env {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func (c Config) GetAppByRepo(repo string) string {
	for app, config := range c.Apps {
		if repo == config.Repo {
			return app
		}
	}
	return ""
}

func (c Config) Events(name string) []string {
	return c.Apps[name].Events
}

func (c Config) Ref(name string) string {
	app := c.Apps[name]

	return fmt.Sprintf("refs/heads/%s", app.Branch)
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


func (h HookHandler) ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)

	if err != nil {
		return body, err
	}

	return body, nil
}

func (h HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := h.config

	if "push" == r.Header.Get("X-Github-Event") {
		b, err := h.ReadBody(r)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		push := (&PushEvent{}).ReadBytes(b)

		name := c.GetAppByRepo(push.Repository.PullsURL)

		if push.Ref == c.Ref(name) {
			c.RestartProcess(name)
		}
	}

	w.WriteHeader(200)
}

func main() {
	h := HookHandler{ReadConfig()}
	http.ListenAndServe(fmt.Sprintf(":%d", h.config.HookPort), h)
}
