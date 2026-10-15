package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type productChild struct {
	process *os.Process
	exit    int
	done    chan struct{}
}

func environment(overrides map[string]string, removed ...string) []string {
	result := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if _, replace := overrides[key]; replace {
			continue
		}
		omit := false
		for _, name := range removed {
			omit = omit || key == name
		}
		if !omit {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func resolvedDirectory(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	for existing := absolute; ; existing = filepath.Dir(existing) {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			relative, err := filepath.Rel(existing, absolute)
			return filepath.Join(resolved, relative), err
		}
		if !os.IsNotExist(err) || filepath.Dir(existing) == existing {
			return "", errors.New("cannot resolve the configured data directory")
		}
	}
}

func insideDirectory(root, name string) bool {
	return strings.EqualFold(root, name) || strings.HasPrefix(strings.ToLower(name)+string(os.PathSeparator), strings.ToLower(root)+string(os.PathSeparator))
}

func runProduct(root string) error {
	root, err := resolvedDirectory(root)
	if err != nil {
		return errors.New("cannot resolve the product directory")
	}
	data := os.Getenv("CODE_AGENT_PORTABLE_DATA_HOME")
	if data == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return errors.New("cannot locate the user data directory")
		}
		data = filepath.Join(base, "CodeOps-Agent")
	}
	data, err = resolvedDirectory(data)
	if err != nil || insideDirectory(root, data) {
		return errors.New("user data must be outside the verified product directory")
	}
	working := os.Getenv("CODE_AGENT_TASK_WORKSPACE_ROOT")
	if working == "" {
		working = filepath.Join(data, "workspaces")
	}
	if !filepath.IsAbs(working) {
		return errors.New("the task workspace root must be an absolute path")
	}
	working, err = resolvedDirectory(working)
	if err != nil || insideDirectory(root, working) {
		return errors.New("the task workspace must be outside the verified product directory")
	}
	if repository := os.Getenv("CODE_AGENT_REPOSITORY_ROOT"); repository != "" {
		repository, err := resolvedDirectory(repository)
		if err != nil || insideDirectory(repository, working) {
			return errors.New("the task workspace must be outside the approved source repository")
		}
	}
	if err := os.MkdirAll(filepath.Join(data, "logs"), 0700); err != nil {
		return errors.New("cannot create the user data directory")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errors.New("cannot create a launch identity")
	}
	identity := hex.EncodeToString(nonce[:])
	logfile, err := os.OpenFile(filepath.Join(data, "logs", "launch-"+identity+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create the launch log")
	}
	defer logfile.Close()
	job, err := newProductJob(data)
	if err != nil {
		return err
	}
	children := []*productChild{}
	defer func() {
		for _, child := range children {
			interruptChild(child.process)
		}
		deadline, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		for _, child := range children {
			select {
			case <-child.done:
			case <-deadline.Done():
				job.close()
			}
		}
		job.close()
		for _, child := range children {
			select {
			case <-child.done:
				fmt.Fprintf(logfile, "child stopped pid=%d exit=%d\n", child.process.Pid, child.exit)
			case <-time.After(5 * time.Second):
				fmt.Fprintf(logfile, "child outcome unknown pid=%d\n", child.process.Pid)
			}
		}
	}()
	start := func(command *exec.Cmd) (*productChild, error) {
		process, err := job.start(command, logfile)
		if err != nil {
			return nil, err
		}
		child := &productChild{process: process, exit: -1, done: make(chan struct{})}
		children = append(children, child)
		fmt.Fprintf(logfile, "child started component=%s pid=%d\n", filepath.Base(command.Path), process.Pid)
		go func() {
			if state, err := process.Wait(); err == nil {
				child.exit = state.ExitCode()
			}
			close(child.done)
		}()
		return child, nil
	}
	serverAddress, err := freeAddress()
	if err != nil {
		return errors.New("cannot allocate a loopback HTTP port")
	}
	pythonAddress, err := freeAddress()
	if err != nil {
		return errors.New("cannot allocate a loopback orchestrator port")
	}
	_, serverPort, _ := net.SplitHostPort(serverAddress)
	_, pythonPort, _ := net.SplitHostPort(pythonAddress)
	path := strings.Join([]string{filepath.Join(root, "git", "cmd"), filepath.Join(root, "git", "ucrt64", "bin"), filepath.Join(os.Getenv("SystemRoot"), "System32"), os.Getenv("SystemRoot")}, string(os.PathListSeparator))
	if os.Getenv("CODE_AGENT_TASK_RUNTIME") == "opensandbox" {
		path += string(os.PathListSeparator) + filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "resources", "bin")
	}
	if err := os.MkdirAll(working, 0700); err != nil {
		return errors.New("cannot prepare the task workspace directory")
	}
	python := exec.Command(filepath.Join(root, "python", "python.exe"), "-I", "-B", "-m", "orchestrator.server", "--host", "127.0.0.1", "--port", pythonPort,
		"--project-root", working, "--working-dir", working, "--memory-dir", filepath.Join(data, "memory"), "--context-backend", "sqlite")
	python.Dir = data
	python.Env = environment(map[string]string{"PATH": path, "CODE_AGENT_MODEL_ADMISSION": "required", "CODE_AGENT_REQUIRE_HARNESS_WORKTREE": "1"},
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODE_AGENT_PROVIDER_CONFIG", "CODE_AGENT_LOCAL_SETUP_PASSWORD", "JWT_SECRET",
		"CODE_AGENT_OPENSANDBOX_API_KEY", "CODE_AGENT_OPENSANDBOX_EXECD_TOKEN", "DOCKER_AUTH_CONFIG")
	if _, err := start(python); err != nil {
		return err
	}
	overrides := map[string]string{"PATH": path, "CODEAGENT_CONFIG": filepath.Join(root, "configs", "local-core.yaml"),
		"CODE_AGENT_SERVER_PORT": serverPort, "CODE_AGENT_ALLOWED_ORIGINS": "http://" + serverAddress,
		"CODE_AGENT_ORCHESTRATOR_ADDR": pythonAddress, "CODE_AGENT_LAUNCH_ID": identity,
		"CODE_AGENT_FRONTEND_DIR": filepath.Join(root, "frontend", "dist"), "CODE_AGENT_LOG_PATH": filepath.Join(data, "logs")}
	for key, name := range map[string]string{"CODE_AGENT_SESSION_LEDGER_PATH": "sessions.sqlite", "CODE_AGENT_LOCAL_IDENTITY_PATH": filepath.Join("identity", "identity.sqlite")} {
		if os.Getenv(key) == "" {
			overrides[key] = filepath.Join(data, name)
		}
	}
	if os.Getenv("CODE_AGENT_REPOSITORY_ROOT") != "" && os.Getenv("CODE_AGENT_TASK_WORKSPACE_ROOT") == "" {
		overrides["CODE_AGENT_TASK_WORKSPACE_ROOT"] = filepath.Join(data, "workspaces")
	}
	if os.Getenv("JWT_SECRET") == "" {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return errors.New("cannot create the process authentication secret")
		}
		overrides["JWT_SECRET"] = hex.EncodeToString(secret[:])
	}
	server := exec.Command(filepath.Join(root, "bin", "code-agent-server.exe"))
	server.Dir, server.Env = data, environment(overrides)
	child, err := start(server)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ready := false
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-child.done:
			return errors.New("the product server exited before readiness; see the launch log")
		default:
		}
		response, err := client.Get("http://" + serverAddress + "/healthz")
		if err == nil {
			ready = response.StatusCode == http.StatusOK && response.Header.Get("X-CodeOps-Instance") == identity
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
		}
		if ready {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ready {
		return errors.New("the owned product server did not become ready; see the launch log")
	}
	url := "http://" + serverAddress
	fmt.Fprintln(logfile, "ready "+url)
	fmt.Println("CodeOps-Agent: " + url)
	fmt.Println("Type exit or close this window to stop the owned product processes. User data: " + data)
	if err := openBrowser(url); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot open the default browser; open the displayed local URL.")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	quit := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if strings.EqualFold(strings.TrimSpace(scanner.Text()), "exit") {
				close(quit)
				return
			}
		}
	}()
	select {
	case <-signals:
	case <-quit:
	case <-child.done:
		return errors.New("the product server stopped; see the launch log")
	}
	return nil
}
