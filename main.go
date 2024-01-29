package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

func Log(name string, format string, input ...any) {
	fmt.Printf("[Tinyhook][%s] %s\n", name, fmt.Sprintf(format, input...))
}

func InitDir(dir string) {
	info, err := os.Stat(dir)
	if err != nil {
		Log("system", "Dir %s doesn't exist creating", dir)
		os.Mkdir(dir, os.ModePerm)
		info, _ = os.Stat(dir)
	}
	if !info.IsDir() {
		Log("system", "%s is not a directory deleting", dir)
		Log("system", "Dir %s doesn't exist creating", dir)
		os.Remove(dir)
		os.Mkdir(dir, os.ModePerm)
	}
}

type Config struct {
	Apps map[string]struct {
		Repo     string            `json:"repo"`
		Branch   string            `json:"branch"`
		Events   []string          `json:"events"`
		Build    []string          `json:"build"`
		Entry    []string          `json:"entry"`
		Env      map[string]string `json:"env"`
	} `json:"apps"`
	ProxyConfig map[string]int `json:"proxy_config"`
	UIPort    int    `json:"ui_port"`
	HookPort  int    `json:"hook_port"`
	ProxyPort int    `json:"proxy_port"`
	Directory string `json:"directory"`
	Processes map[string]*os.Process
}

func (c Config) Logger(app string, command string) *os.File {
	f := strings.Join(strings.Split(command, " "), "-")
	n := fmt.Sprintf("%s.%s.%s.log", app, f, time.Now().UTC())
	p := fmt.Sprintf("%s/.log/%s", c.Directory, n)

	l, err := os.Create(p)

	if err != nil {
		Log(app, "Couldn't Open Log %s Piping to STDERR", p)
		return os.Stderr
	}

	return l
}

func (c Config) RepoUrl(name string) *url.URL {
	app := c.Apps[name]

	u, err := url.Parse(app.Repo)
	if err != nil {
		Log(name, "Invalid Repo URL")
	}
	return u
}


func (c Config) AppDir(name string) string {
	return fmt.Sprintf("%s/%s", c.Directory, c.RepoUrl(name).Path)
}

func (c Config) LogDirectory() string {
	return fmt.Sprintf("%s/.log", c.Directory)
}

func (c *Config) Init() Config {
	if c.Directory == "" {
		c.Directory = ".tinyhook"
	}
	InitDir(c.Directory)
	InitDir(c.LogDirectory())

	if c.Processes == nil {
		c.Processes = map[string]*os.Process{}
	}

	Log("system", "Now starting %d process(es)", len(c.Apps))
	for name := range c.Apps {
		c.StartProcess(name)
	}
	return *c
}

func (c Config) PushProcess(name string, proc *os.Process) {
	c.Processes[name] = proc
}

func (c Config) StartProcess(name string) {
	Log(name, "Starting process")
	c.Clone(name)
	c.Checkout(name)
	c.Pull(name)
	c.RunBuild(name)
	c.RunEntry(name)
}

func (c Config) Clone(name string) {
	_, err := os.Stat(c.AppDir(name) + "/.git")
	if err != nil {
		Log(name, "No GIT Repo Detected")
		out := c.Logger(name, "git clone")
		app := c.Apps[name]
		dir := c.AppDir(name)
		cmd := exec.Command("git", "clone", app.Repo, dir)
		cmd.Stderr = out
		cmd.Stdout = out
		Log(name, "Cloning from %s into %s", app.Repo, dir)
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
	Log(name, "Checkout branch %s", app.Branch)
	cmd.Run()
}

func (c Config) Pull(name string) {
	out := c.Logger(name, "git pull")
	app := c.Apps[name]
	cmd := exec.Command("git", "pull")
	cmd.Dir = c.AppDir(name)
	cmd.Stdout = out
	cmd.Stderr = out
	Log(name, "Pull latest from %s", app.Repo)
	cmd.Run()
}

func (c Config) RunBuild(name string) {
	out := c.Logger(name, "build")
	app := c.Apps[name]
	cmd := exec.Command(app.Build[0], app.Build[1:]...)
	cmd.Dir = c.AppDir(name)
	cmd.Stderr = out
	cmd.Stdout = out
	Log(name, "Runninf build command '%s'", strings.Join(app.Build, " "))
	cmd.Run()
}

func (c Config) RunEntry(name string) {
	out := c.Logger(name, "entry")
	app := c.Apps[name]
	cmd := exec.Command(app.Entry[0], app.Entry[1:]...)
	cmd.Dir = c.AppDir(name)
	cmd.Stderr = out
	cmd.Stdout = out
	cmd.Env = c.BuildEnv(name)
	Log(name, "Starting entrypoint '%s'", strings.Join(app.Entry, " "))
	cmd.Start()
	c.PushProcess(name, cmd.Process)
}

func (c Config) Kill(name string) {
	proc := c.Processes[name]
	if proc != nil {
		out := c.Logger(name, "kill")
		pid := fmt.Sprintf("%d", proc.Pid)
		cmd := exec.Command("kill", pid)
		cmd.Stdout = out
		cmd.Stderr = out
		Log(name, "Killing Process %s", pid)
		cmd.Run()
	}
}

func (c Config) RestartProcess(name string) {
	Log(name, "Restarting")
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
		Log("system", "No config found!")
		os.Exit(1)
	}
	err = json.Unmarshal(j, &c)
	if err != nil {
		Log("system", "error reading config: %v", fmt.Sprintf("%v", err))
		os.Exit(1)
	}
	return c.Init()
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
			Log(name, "PUSH Detected at %s", push.Repository.CloneURL)
			c.RestartProcess(name)
		}
	}

	w.WriteHeader(200)
}

type ProxyHandler struct {
	config Config
}

func (p ProxyHandler) ServeHTTP (w http.ResponseWriter, r *http.Request) {
	host := r.URL.Host
	port := p.config.ProxyConfig[host]


	Log("server:proxy", r.URL.Hostname())
	Log("server:proxy", "request received for host %s", host)

	if port == 0 {
		Log("server:proxy", "No configured service for %s", host)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	url, e := url.Parse(fmt.Sprintf("http://localhost:%d", port))

	if e != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	r.Host = url.Host
	r.URL.Host = url.Host
	r.URL.Scheme = url.Scheme
	r.RequestURI = ""

	res, e := http.DefaultClient.Do(r)

	if e != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for header, value := range res.Header {
		w.Header()[header] = value
	}

	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

func main() {
	c := ReadConfig()
	h := HookHandler{c}
	p := ProxyHandler{c}

	sig := make(chan string, 1)

	go func () {
		Log("server:hook", "Now listening at localhost:%d", c.HookPort)
		err := http.ListenAndServe(fmt.Sprintf(":%d", c.HookPort), h)
		Log("server:hook", "%v", err)
		sig <- "server:hook"
	}()

	go func () {
		Log("server:proxy", "Now listening at localhost:%d", c.ProxyPort)
		err := http.ListenAndServe(fmt.Sprintf(":%d", c.ProxyPort), p)
		Log("server:proxy", "%v", err)
		sig <- "server:proxy"
	}()

	server := <- sig

	Log("system", "%s has stopped unexpectedly. shutting down.", server)

	os.Exit(1)
}
