package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/session"
	"code-agent/pkg/token"

	"github.com/gorilla/websocket"
)

func TestProductionLocalCoreStartsWithoutOptionalServices(t *testing.T) {
	fixture := startProductionLocalCore(t)
	base, client := fixture.base, fixture.client
	response, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
}

func TestProductionLocalCoreTokenAdmissionProjection(t *testing.T) {
	fixture := startProductionLocalCore(t)
	fixture.authenticate(t)
	path := filepath.Join(fixture.dir, "sessions.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	before, _ := ledger.SessionIDs(context.Background())
	read := func() (string, string, string, int64, int64) {
		t.Helper()
		response, err := fixture.client.Get(fixture.base + "/api/v1/capabilities")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Data map[string]struct {
				State  string
				Tokens struct {
					BatchID    string `json:"batch_id"`
					Limit      int64
					Used       int64
					Reserved   int64
					CostStatus string `json:"cost_status"`
				}
			}
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
			t.Fatal("authenticated capabilities unavailable")
		}
		budget := result.Data["budget"]
		if budget.Tokens.Limit != 100_000_000 || budget.Tokens.CostStatus != "unknown" || result.Data["execution"].State != "blocked" {
			t.Fatal("token projection invented money or admitted execution")
		}
		return result.Data["provider"].State, budget.State, budget.Tokens.BatchID, budget.Tokens.Used, budget.Tokens.Reserved
	}
	provider, state, id, _, _ := read()
	if provider != "blocked" || state != "unknown" || id != "" {
		t.Fatal("missing model or batch was reported ready")
	}
	after, _ := ledger.SessionIDs(context.Background())
	if len(after) != len(before) {
		t.Fatal("capability reads created a competing or mutable budget")
	}
	batch, err := admission.NewBudget(ledger).Reserve(context.Background(), 7, "task-a", "call-a", 103)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admission.NewBudget(ledger).MarkUnknown(context.Background(), 7, "call-a"); err != nil {
		t.Fatal(err)
	}
	_, state, id, used, reserved := read()
	if state != "blocked" || id != batch.ID || used != 0 || reserved != 103 {
		t.Fatal("unconfirmed reservation was hidden from the product")
	}
	fixture.stop(t)
	fixture.start(t)
	_, state, id, used, reserved = read()
	if state != "blocked" || id != batch.ID || used != 0 || reserved != 103 {
		t.Fatal("product restart reset the held reservation")
	}
}

func TestProductionLocalCoreOwnerAndWebSocketTicket(t *testing.T) {
	fixture := startProductionLocalCore(t)
	fixture.authenticate(t)
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(fixture.dir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := session.NewWorkbench(ledger, nil).Create(context.Background(), 7, "foreign", "private history", "")
	ledger.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/events", "/ws-ticket"} {
		request, _ := http.NewRequest(http.MethodGet, fixture.base+"/api/v1/sessions/"+foreign.ID+suffix, nil)
		if suffix == "/ws-ticket" {
			request.Method = http.MethodPost
		}
		response, err := fixture.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("foreign Session%s was not refused: %d", suffix, response.StatusCode)
		}
	}
	response, err := fixture.client.Post(fixture.base+"/api/v1/sessions", "application/json", strings.NewReader(`{"projectName":"core","title":"owned"}`))
	if err != nil {
		t.Fatal(err)
	}
	var owned struct{ Data session.SessionView }
	err = json.NewDecoder(response.Body).Decode(&owned)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("owned Session unavailable")
	}
	response, err = fixture.client.Post(fixture.base+"/api/v1/sessions/"+owned.Data.ID+"/ws-ticket", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct{ Data struct{ Ticket string } }
	err = json.NewDecoder(response.Body).Decode(&ticket)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || ticket.Data.Ticket == "" {
		t.Fatal("owned websocket ticket unavailable")
	}
	address := strings.Replace(fixture.base, "http://", "ws://", 1) + "/api/v1/sessions/" + owned.Data.ID + "/ws?ticket=" + url.QueryEscape(ticket.Data.Ticket)
	connection, _, err := websocket.DefaultDialer.Dial(address, http.Header{"Origin": []string{fixture.base}})
	if err != nil {
		t.Fatal("owned websocket ticket did not connect")
	}
	connection.Close()
	connection, response, err = websocket.DefaultDialer.Dial(address, http.Header{"Origin": []string{fixture.base}})
	if connection != nil {
		connection.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("one-time websocket ticket could be replayed")
	}
	response.Body.Close()
	response, err = fixture.client.Get(fixture.base + "/api/v1/users/me")
	if err != nil {
		t.Fatal(err)
	}
	var profile struct{ Data struct{ ID uint } }
	err = json.NewDecoder(response.Body).Decode(&profile)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("owned identity unavailable")
	}
	ledger, err = session.OpenSQLiteEventLog(filepath.Join(fixture.dir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	workbench := session.NewWorkbench(ledger, nil)
	message, err := workbench.AppendUserMessage(context.Background(), profile.Data.ID, owned.Data.ID, int64(owned.Data.EventCount), "saved legacy message")
	if err != nil {
		ledger.Close()
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(context.Background(), profile.Data.ID, owned.Data.ID, int64(owned.Data.EventCount+1), message.ID, "legacy checkpoint")
	if err != nil {
		ledger.Close()
		t.Fatal(err)
	}
	err = workbench.UpdateStatus(context.Background(), profile.Data.ID, owned.Data.ID, int64(owned.Data.EventCount+2), "paused")
	ledger.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{"", "blocked-continue"} {
		body, _ := json.Marshal(map[string]any{"requestId": requestID, "checkpointHash": checkpoint.Hash, "expectedSeq": owned.Data.EventCount + 3})
		response, err := fixture.client.Post(fixture.base+"/api/v1/sessions/"+owned.Data.ID+"/continue", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unadmitted continuation with requestId %q = %d, want 503", requestID, response.StatusCode)
		}
	}
	response, err = fixture.client.Get(fixture.base + "/api/v1/sessions/" + owned.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	var unchanged struct{ Data session.SessionView }
	err = json.NewDecoder(response.Body).Decode(&unchanged)
	response.Body.Close()
	if err != nil || unchanged.Data.EventCount != owned.Data.EventCount+3 {
		t.Fatal("refused continuation changed the canonical Session Ledger")
	}
}

func TestProductionLocalCoreServesExistingBrowserSurface(t *testing.T) {
	fixture := startProductionLocalCore(t)
	response, err := fixture.client.Get(fixture.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`id="root"`)) || !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("built browser Surface is unavailable (status %d, read error %v)", response.StatusCode, err)
	}
	response.Body.Close()
	response, err = fixture.client.Get(fixture.base + "/assets/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("browser asset directory listing is enabled")
	}
}

func TestProductionLocalCoreIdentityAndHistory(t *testing.T) {
	fixture := startProductionLocalCore(t)
	base, client := fixture.base, fixture.client
	response, err := client.Get(base + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated history = %d, want 401", response.StatusCode)
	}
	fixture.authenticate(t)
	response, err = client.Post(base+"/api/v1/sessions", "application/json", strings.NewReader(`{"projectName":"core-test","title":"durable history"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create Session = %d", response.StatusCode)
	}
	response, err = client.Get(base + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("durable history")) {
		t.Fatalf("authenticated history missing (status %d, read error %v)", response.StatusCode, err)
	}
	response.Body.Close()
	response, err = client.Get(base + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var capabilities struct {
		Data map[string]struct{ State string }
	}
	if err := json.NewDecoder(response.Body).Decode(&capabilities); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || capabilities.Data["history"].State != "ready" || capabilities.Data["execution"].State != "blocked" || capabilities.Data["rag"].State != "degraded" || capabilities.Data["trace"].State != "unknown" {
		t.Fatal("capability states are not independently reported")
	}
	for _, path := range []string{"/search", "/documents/accessible", "/upload/check", "/chat", "/memory/retrieve"} {
		response, err := client.Post(base+"/api/v1"+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("disabled optional capability %s = %d, want 503", path, response.StatusCode)
		}
	}
	response, err = client.Get(base + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	var sessions struct {
		Data []struct {
			ID         string
			EventCount int
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(sessions.Data) != 1 {
		t.Fatal("expected one persisted Session")
	}
	requestBody, _ := json.Marshal(map[string]any{"requestId": "blocked-probe", "content": "write a file", "expectedSeq": sessions.Data[0].EventCount})
	response, err = client.Post(base+"/api/v1/sessions/"+sessions.Data[0].ID+"/messages", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unadmitted execution = %d, want 503", response.StatusCode)
	}
	response.Body.Close()
	fixture.stop(t)
	fixture.start(t)
	response, err = client.Get(base + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("durable history")) {
		t.Fatalf("history or persisted cookie identity did not survive process restart (status %d)", response.StatusCode)
	}
	response.Body.Close()
	address, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	oldCookies := client.Jar.Cookies(address)
	response, err = client.Post(base+"/api/v1/users/logout", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", response.StatusCode)
	}
	fixture.stop(t)
	fixture.start(t)
	client.Jar.SetCookies(address, oldCookies)
	for _, path := range []string{"/api/v1/sessions", "/api/v1/auth/refreshToken"} {
		if strings.Contains(path, "refreshToken") {
			response, err = client.Post(base+path, "application/json", strings.NewReader(`{}`))
		} else {
			response, err = client.Get(base + path)
		}
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("revoked credentials usable after restart at %s: %d", path, response.StatusCode)
		}
	}
}

func TestProductionLocalCoreRejectsMismatchedIdentity(t *testing.T) {
	fixture := startProductionLocalCore(t)
	key, base, client := fixture.key, fixture.base, fixture.client
	fixture.authenticate(t)
	response, err := client.Get(base + "/api/v1/users/me")
	if err != nil {
		t.Fatal(err)
	}
	var profile struct{ Data struct{ ID uint } }
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	wrong, err := token.NewJWTManager(key, 1, 1).GenerateToken(profile.Data.ID+1, "operator@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, base+"/api/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+wrong)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JWT identity inconsistent with persisted identity = %d, want 401", response.StatusCode)
	}
	wrongRefresh, err := token.NewJWTManager(key, 1, 1).GenerateRefreshToken(profile.Data.ID+1, "operator@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"refreshToken": wrongRefresh})
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Post(base+"/api/v1/auth/refreshToken", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh with mismatched persisted identity = %d, want 401", response.StatusCode)
	}
}

type localCoreProcess struct {
	binary, dir, config, password, key, base string
	environment                              []string
	client                                   *http.Client
	command                                  *exec.Cmd
	done                                     chan error
	output                                   bytes.Buffer
}

func (fixture *localCoreProcess) authenticate(t *testing.T) {
	t.Helper()
	for _, path := range []string{"register", "login"} {
		body, _ := json.Marshal(map[string]string{"email": "operator@example.test", "password": fixture.password})
		response, err := fixture.client.Post(fixture.base+"/api/v1/users/"+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, response.StatusCode)
		}
	}
}

func TestProductionLocalCoreRejectsForeignBrowserOrigin(t *testing.T) {
	fixture := startProductionLocalCore(t)
	fixture.authenticate(t)
	for _, variant := range []string{"origin", "host"} {
		request, err := http.NewRequest(http.MethodGet, fixture.base+"/api/v1/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		if variant == "origin" {
			request.Header.Set("Origin", "https://untrusted.example")
		} else {
			request.Host = "untrusted.example"
		}
		response, err := fixture.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("untrusted %s request = %d, want 403", variant, response.StatusCode)
		}
	}
}

func startProductionLocalCore(t *testing.T) *localCoreProcess {
	t.Helper()
	if os.Getenv("CODE_AGENT_RUN_LOCAL_CORE_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_LOCAL_CORE_E2E=1 for the production HTTP entry")
	}
	root := e2ERepositoryRoot(t)
	taskDir := t.TempDir()
	binary := filepath.Join(taskDir, "server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/server")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production server: %v: %s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(taskDir, "local.yaml")
	config := fmt.Sprintf("server:\n  profile: local-core\n  port: %q\n  mode: test\n  allowed_origins: http://127.0.0.1:%d\n  frontend_dir: %q\nharness:\n  session_ledger_path: %q\n  identity_path: %q\njwt:\n  secret: ${JWT_SECRET:}\n  access_token_expire_hours: 1\n  refresh_token_expire_days: 1\nlog:\n  level: error\n  format: json\n  output_path: stdout\n", fmt.Sprint(port), port, filepath.ToSlash(filepath.Join(root, "frontend", "dist")), filepath.ToSlash(filepath.Join(taskDir, "sessions.sqlite")), filepath.ToSlash(filepath.Join(taskDir, "identity", "identity.sqlite")))
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	fixture := &localCoreProcess{binary: binary, dir: taskDir, config: configPath}
	for _, entry := range os.Environ() {
		entryName := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(entryName, "MYSQL_") || strings.HasPrefix(entryName, "REDIS_") || strings.HasPrefix(entryName, "MINIO_") || entryName == "JWT_SECRET" || strings.HasPrefix(entryName, "CODEAGENT_") || strings.HasPrefix(entryName, "CODE_AGENT_") || strings.HasPrefix(entryName, "OPENAI_") || strings.HasPrefix(entryName, "ANTHROPIC_") || strings.HasPrefix(entryName, "LLM_") || strings.HasPrefix(entryName, "OTEL_") {
			continue
		}
		fixture.environment = append(fixture.environment, entry)
	}
	password := hex.EncodeToString(secret[:16])
	fixture.password, fixture.key = password, hex.EncodeToString(secret[:])
	fixture.environment = append(fixture.environment, "CODEAGENT_CONFIG="+configPath, "JWT_SECRET="+fixture.key, "CODE_AGENT_LOCAL_SETUP_USER=operator@example.test", "CODE_AGENT_LOCAL_SETUP_PASSWORD="+password)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.client = &http.Client{Timeout: time.Second, Jar: jar}
	fixture.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Cleanup(func() { fixture.stop(t) })
	fixture.start(t)
	return fixture
}

func (fixture *localCoreProcess) stop(t *testing.T) {
	t.Helper()
	if fixture.command == nil {
		return
	}
	_ = fixture.command.Process.Kill()
	select {
	case <-fixture.done:
	case <-time.After(5 * time.Second):
		t.Fatal("production server did not stop")
	}
	fixture.command = nil
}

func (fixture *localCoreProcess) start(t *testing.T) {
	t.Helper()
	fixture.output.Reset()
	command := exec.Command(fixture.binary)
	command.Dir, command.Env = fixture.dir, fixture.environment
	command.Stdout, command.Stderr = &fixture.output, &fixture.output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	fixture.command, fixture.done = command, make(chan error, 1)
	go func() { fixture.done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case exit := <-fixture.done:
			fixture.done <- exit
			t.Fatalf("local core exited before serving HTTP: %v; %s", exit, fixture.output.String())
		default:
		}
		response, err := fixture.client.Get(fixture.base + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("local core did not serve HTTP without optional services")
}
