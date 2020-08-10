package main

import (
	"context"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os/exec"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func NewDR(runnerUUID string) *DockerRunner {
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
		RunnerUUID:   runnerUUID,
	}
}

type DockerRunner struct {
	DockerBinary string
	Volume       string
	RunnerUUID   string
}

func (dr *DockerRunner) StartRunnerContainer(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "run", "-d",
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
	Input  string
	Output string
}

type WebApp struct {
	Runner *DockerRunner
}

func (wa *WebApp) Editor(w http.ResponseWriter, r *http.Request) {
	var (
		response Response
	)

	response.Input = "Enter text here..."

	if r.Method == "POST" {
		err := r.ParseForm()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		response = Response{
			Input:  r.Form.Get("comment"),
			Output: "lol",
		}
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

/*
	dr := NewDR("da8f91bf-f8f6-42fb-a9db-cc25c0e564d8")

	cmd := dr.StartRunnerContainer(context.Background())
	out := dr.GetContainerOutput(cmd)
	containerID := strings.TrimRight(out, "\n")
	time.Sleep(3 * time.Second) // nolint
	fmt.Printf("`%s`\n", containerID)
	cmd = dr.StopContainer(context.Background(), containerID)
	out = dr.GetContainerOutput(cmd)
	fmt.Println(out)
*/

func main() {
	wa := &WebApp{Runner: NewDR("123-124")}
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
