package main

import (
	"context"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

const (
	RunnerTimeout = 3 * time.Second
	StopTimeout   = 3 * time.Second
)

func NewDR() *DockerRunner {
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
	}
}

type DockerRunner struct {
	DockerBinary string
	Volume       string
	RunnerUUID   string
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
	out := dr.GetContainerOutput(dr.ListContainersCmd(ctx))
	result = append(result, strings.Split(out, "\n")...)

	return
}

func (dr *DockerRunner) StartRunnerContainer(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "run", "-d",
		fmt.Sprintf("--name=oac-%s", dr.RunnerUUID),
		"--network=none", "--memory=128m", "--memory-swap=128m",
		"--read-only",
		"-v", fmt.Sprintf("%s:/data:ro", dr.Volume),
		"alpine", "sh",
		"-c", "while :; do sleep 1; done")
}

func (dr *DockerRunner) StartTaskContainer(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "run",
		"-v", fmt.Sprintf("%s:/data", dr.Volume),
		"oac-task", "/oac-downloader", dr.RunnerUUID,
	)
}

func (dr *DockerRunner) StopContainer(ctx context.Context, containerID string) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "stop",
		"-t", "1", containerID)
}

func (dr *DockerRunner) GetContainerOutput(cmd *exec.Cmd) string {
	out, err := cmd.StdoutPipe()

	if err != nil {
		panic(err)
	}

	err = cmd.Start()
	if err != nil {
		panic(err)
	}

	output, err := ioutil.ReadAll(out)
	if err != nil {
		panic(err)
	}

	err = cmd.Wait()

	if err != nil {
		panic(err)
	}

	return string(output)
}

type Response struct {
	ID     string
	Input  string
	Output string
}

type WebApp struct {
	Runner *DockerRunner
}

func (wa *WebApp) Editor(w http.ResponseWriter, r *http.Request) { // nolint:funlen
	var (
		response Response
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

		wa.Runner.SetRunnerUUID(runnerUUID)
		// Run here
		runnerCtx, runnerCancel := context.WithTimeout(context.Background(), RunnerTimeout)
		defer runnerCancel()

		cmd := wa.Runner.StartRunnerContainer(runnerCtx)
		containerID := wa.Runner.GetContainerOutput(cmd)

		// Set output
		response = Response{
			ID:     runnerUUID,
			Input:  r.Form.Get("comment"),
			Output: "lol",
		}
		// Stop container
		ctx, cancel := context.WithTimeout(context.Background(), StopTimeout)
		defer cancel()

		cmd = wa.Runner.StopContainer(ctx, strings.TrimRight(containerID, "\n"))
		out := wa.Runner.GetContainerOutput(cmd)
		fmt.Println("Stop output: ", out)
	}

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
	runner := NewDR()
	ctx, cancel := context.WithTimeout(context.Background(), StopTimeout)

	defer cancel()

	for _, cid := range runner.ListContainers(ctx) {
		if strings.HasPrefix(cid, "oac-") {
			cmd := runner.StopContainer(ctx, cid)
			out := runner.GetContainerOutput(cmd)
			fmt.Println(out)
		}
	}

	wa := &WebApp{Runner: NewDR()}
	r := mux.NewRouter()
	r.HandleFunc("/", wa.IndexPage)
	r.HandleFunc("/index.htm", wa.IndexPage)
	r.HandleFunc("/index.html", wa.IndexPage)
	r.HandleFunc("/editor", wa.Editor).Methods("POST")
	r.HandleFunc("/editor", wa.Editor).Methods("GET")

	srv := http.Server{Addr: "127.0.0.1:8000", Handler: r}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
