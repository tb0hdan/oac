package main

import (
	"context"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/tb0hdan/memcache"
)

const (
	RunnerTimeout = 3 * time.Second
	StopTimeout   = 3 * time.Second
)

func NewDR(myIP string, cache *memcache.CacheType) *DockerRunner {
	dockerBinary, lookErr := exec.LookPath("docker")
	if lookErr != nil {
		panic(lookErr)
	}

	id, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}

	return &DockerRunner{
		DockerBinary: dockerBinary,
		Volume:       id.String(),
		RunnerUUID:   "",
		MyIP:         myIP,
		Cache:        cache,
	}
}

type DockerRunner struct {
	DockerBinary string
	Volume       string
	RunnerUUID   string
	MyIP         string
	Cache        *memcache.CacheType
}

func (dr *DockerRunner) SetRunnerUUID(runnerUUID string) {
	fmt.Println("Setting runner UUID to", runnerUUID)
	dr.RunnerUUID = runnerUUID
}

func (dr *DockerRunner) ListContainersCmd(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "ps",
		"--format", "{{.Names}}")
}

func (dr *DockerRunner) ListContainers(ctx context.Context) (result []string) {
	out, err := dr.GetContainerOutput(dr.ListContainersCmd(ctx))
	if err != nil {
		panic(err)
	}

	result = append(result, strings.Split(out, "\n")...)

	return
}

func (dr *DockerRunner) StartRunnerContainer(ctx context.Context) *exec.Cmd {
	image := "python:3.7-alpine"

	return exec.CommandContext(ctx, dr.DockerBinary, "run", "-d",
		fmt.Sprintf("--name=oac-%s", dr.RunnerUUID),
		"--network=none", "--memory=128m", "--memory-swap=128m",
		"--read-only",
		"-v", fmt.Sprintf("%s:/data:ro", dr.Volume),
		image, "sh",
		"-c", "while :; do sleep 1; done")
}

func (dr *DockerRunner) StartTaskInsideContainer(ctx context.Context, containerID string) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "exec",
		containerID,
		"python", "/data/script")
}

func (dr *DockerRunner) StartTaskContainer(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "run",
		"-v", fmt.Sprintf("%s:/data", dr.Volume),
		"oac-task", "wget", "-O", "/data/script",
		fmt.Sprintf("http://%s/static/%s", dr.MyIP, dr.RunnerUUID),
	)
}

func (dr *DockerRunner) StopContainer(ctx context.Context, containerID string) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "stop",
		"-t", "1", containerID)
}

func (dr *DockerRunner) GetContainerOutput(cmd *exec.Cmd) (string, error) {
	out, err := cmd.StdoutPipe()

	if err != nil {
		return "", err
	}

	err = cmd.Start()
	if err != nil {
		return "", err
	}

	output, err := ioutil.ReadAll(out)
	if err != nil {
		return "", err
	}

	err = cmd.Wait()

	if err != nil {
		return "", err
	}

	return string(output), nil
}

type Response struct {
	ID     string
	Input  string
	Output string
}

type WebApp struct {
	Runner *DockerRunner
}

func (wa *WebApp) writeCode(runnerUUID, code string) error {
	f, err := os.Create(path.Join("static", runnerUUID))

	if err != nil {
		return err
	}

	_, err = f.Write([]byte(code + "\n"))

	if err != nil {
		return err
	}

	err = f.Sync()

	if err != nil {
		return err
	}

	defer f.Close()

	return nil
}

func (wa *WebApp) Editor(w http.ResponseWriter, r *http.Request) { // nolint:funlen
	var (
		response    Response
		containerID string
	)

	if len(response.ID) == 0 {
		id, err := uuid.NewRandom()
		if err != nil {
			panic(err)
		}

		response.ID = id.String()
	}

	response.Input = "Enter text here..."

	if r.Method == "POST" {
		err := r.ParseForm()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		runnerUUID := r.Form.Get("id")

		if len(runnerUUID) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Save code
		code := r.Form.Get("comment")
		//
		err = wa.writeCode(runnerUUID, code)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		//
		wa.Runner.SetRunnerUUID(runnerUUID)

		if cid, ok := wa.Runner.Cache.Get(runnerUUID); !ok {
			// start container
			// Run here
			runnerCtx, runnerCancel := context.WithTimeout(context.Background(), RunnerTimeout)
			defer runnerCancel()

			cmd := wa.Runner.StartRunnerContainer(runnerCtx)
			containerID, err := wa.Runner.GetContainerOutput(cmd)
			containerID = strings.TrimRight(containerID, "\n")
			fmt.Println("Start: ", containerID, err)
			wa.Runner.Cache.Set(runnerUUID, containerID)
		} else {
			containerID = cid.(string)
		}

		fmt.Println("Cache: ", containerID)
		// get script
		taskCtx, taskCancel := context.WithTimeout(context.Background(), RunnerTimeout)
		defer taskCancel()

		cmd := wa.Runner.StartTaskContainer(taskCtx)
		out, err := wa.Runner.GetContainerOutput(cmd)
		fmt.Println(out, err)

		// Get task output
		taskInCtx, taskInCancel := context.WithTimeout(context.Background(), RunnerTimeout)
		defer taskInCancel()

		if len(containerID) == 0 {
			http.Error(w, "no container id", http.StatusBadRequest)
			return
		}

		cmdX := wa.Runner.StartTaskInsideContainer(taskInCtx, containerID)
		taskOut, err := wa.Runner.GetContainerOutput(cmdX)
		fmt.Println(containerID, taskOut+"\n"+err.Error())
		// Set output
		response = Response{
			ID:     runnerUUID,
			Input:  code,
			Output: taskOut + "\n" + err.Error(),
		}
		// Stop container
		/*
			ctx, cancel := context.WithTimeout(context.Background(), StopTimeout)
			defer cancel()

			cmd = wa.Runner.StopContainer(ctx, strings.TrimRight(containerID, "\n"))
			out, err = wa.Runner.GetContainerOutput(cmd)
			fmt.Println("Stop output: ", out, err) */
	} // nolint

	w.Header().Add("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	template := template.Must(template.ParseFiles("editor.html"))

	err := template.Execute(w, response)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

func (wa *WebApp) IndexPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	t := template.Must(template.ParseFiles("index.html"))
	err := t.Execute(w, nil)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

func main() {
	cache := memcache.New(log.New())
	runner := NewDR("192.168.3.247:8000", cache)
	ctx, cancel := context.WithTimeout(context.Background(), StopTimeout)

	defer cancel()

	for _, cid := range runner.ListContainers(ctx) {
		if strings.HasPrefix(cid, "oac-") {
			cmd := runner.StopContainer(ctx, cid)
			out, err := runner.GetContainerOutput(cmd)

			if err != nil {
				panic(err)
			}

			fmt.Println(out)
		}
	}

	wa := &WebApp{Runner: runner}
	r := mux.NewRouter()

	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	r.HandleFunc("/", wa.IndexPage)
	r.HandleFunc("/index.htm", wa.IndexPage)
	r.HandleFunc("/index.html", wa.IndexPage)
	r.HandleFunc("/editor", wa.Editor).Methods("POST")
	r.HandleFunc("/editor", wa.Editor).Methods("GET")

	srv := http.Server{Addr: "0.0.0.0:8000", Handler: r}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
