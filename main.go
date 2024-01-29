package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
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
	Loggers      map[string]*os.File
}

func (c Config) Logger(app string, command string) *os.File {
	f := strings.Join(strings.Split(command, " "), "-")
	n := fmt.Sprintf("%s.%s.%s.log", app, f, time.Now().Format(time.UnixDate))
	p := fmt.Sprintf("%s/.log/%s", c.Directory, n)

	l, err := os.OpenFile(p, os.O_CREATE, fs.ModePerm)

	if err != nil {
		log.Printf("Couldn't Open Log %s Piping to STDERR", p)
		return os.Stderr
	}

	return l
}

func (c Config) RepoUrl(name string) *url.URL {
	app := c.Apps[name]

	u, err := url.Parse(app.Repo)
	if err != nil {
		log.Fatal("Invalid Repo URL")
	}
	return u
}

func (c Config) AppDir(name string) string {
	return fmt.Sprintf("%s/%s", c.Directory, c.RepoUrl(name).Path)
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
	log_dir := fmt.Sprintf("%s/.log", c.Directory)
	info, err = os.Stat(log_dir)
	if err != nil {
		os.Mkdir(log_dir, os.ModePerm)
		info, _ = os.Stat(c.Directory)
	}
	if !info.IsDir() {
		os.Remove(log_dir)
		os.Mkdir(log_dir, os.ModePerm)
	}
	if c.Processes == nil {
		c.Processes = map[string]*os.Process{}
	}
	if c.Loggers == nil {
		c.Loggers = map[string]*os.File{}
	}
	for name := range c.Apps {
		c.StartProcess(name)
	}
}

func (c Config) PushProcess(name string, proc *os.Process) {
	c.Processes[name] = proc
}

func (c Config) PushLogger(name string, out *os.File) {
	c.Loggers[name] = out
}

func (c Config) StartProcess(name string) {
	c.Clone(name)
	c.Checkout(name)
	c.Pull(name)
	c.RunBuild(name)
	c.RunEntry(name)
}

func (c Config) Clone(name string) {
	_, err := os.Stat(c.AppDir(name) + "/.git")
	if err != nil {
		log.Printf("No GIT Repo Detected for %s", name)
		app := c.Apps[name]
		dir := c.AppDir(name)
		cmd := exec.Command("git", "clone", app.Repo, dir)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		log.Printf("Cloning %s from %s into %s", name, app.Repo, dir)
		cmd.Run()
	}
}

func (c Config) Checkout(name string) {
	out := c.Logger(name, "git checkout")
	app := c.Apps[name]
	cmd := exec.Command("git", "checkout", app.Branch)
	cmd.Stderr = out
	cmd.Stdout = out
	cmd.Dir = c.AppDir(name)
	log.Printf("now running git checkout %s", app.Branch)
	cmd.Run()
	out.Close()
}

func (c Config) Pull(name string) {
	out := c.Logger("name", "git pull")
	app := c.Apps[name]
	cmd := exec.Command("git", "pull")
	cmd.Dir = c.AppDir(name)
	cmd.Stdout = out
	cmd.Stderr = out
	log.Printf("Pulling latest from %s", app.Repo)
	cmd.Run()
	out.Close()
}

func (c Config) RunBuild(name string) {
	out := c.Logger(name, "build")
	app := c.Apps[name]
	cmd := exec.Command(app.Build[0], app.Build[1:]...)
	cmd.Dir = c.AppDir(name)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("now running %s", strings.Join(app.Build, " "))
	cmd.Run()
	out.Close()
}

func (c Config) RunEntry(name string) {
	app := c.Apps[name]
	cmd := exec.Command(app.Entry[0], app.Entry[1:]...)
	cmd.Dir = c.AppDir(name)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Env = c.BuildEnv(name)
	log.Printf("Now starting %s from entry %s", name, strings.Join(app.Entry, " "))
	cmd.Start()

	c.PushProcess(name, cmd.Process)
}

func (c Config) Kill(name string) {
	proc := c.Processes[name]
	if proc != nil {
		log.Printf("Killing Process %d", proc.Pid)
		cmd := exec.Command("kill", fmt.Sprintf("%d", proc.Pid))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
	out := c.Loggers[name]
	if out != nil {
		out.Close()
	}
}

func (c Config) RestartProcess(name string) {
	log.Printf("Restarting %s", name)
	c.Kill(name)
	c.Checkout(name)
	c.Pull(name)
	c.RunBuild(name)
	c.RunEntry(name)
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

		name := c.GetAppByRepo(push.Repository.CloneURL)

		if push.Ref == c.Ref(name) {
			log.Printf("PUSH Detected on %s @ %s", push.Repository.FullName, push.Ref)
			c.RestartProcess(name)
		}
	}

	w.WriteHeader(200)
}

func main() {
	h := HookHandler{ReadConfig()}
	http.ListenAndServe(fmt.Sprintf(":%d", h.config.HookPort), h)
}
