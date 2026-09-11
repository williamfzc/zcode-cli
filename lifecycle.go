// App lifecycle commands (status/start/stop): launch ZCode through `open -a`
// with the CDP debug port, gather status, and quit gracefully. Business
// commands never restart the app.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type statusReport struct {
	AppRunning      bool         `json:"appRunning"`
	DebugPortReady  bool         `json:"debugPortReady"`
	Port            int          `json:"port"`
	Browser         string       `json:"browser,omitempty"`
	ProtocolVersion string       `json:"protocolVersion,omitempty"`
	UserAgent       string       `json:"userAgent,omitempty"`
	WebsocketURL    string       `json:"websocketUrl,omitempty"`
	PageTargets     int          `json:"pageTargets"`
	Targets         []targetInfo `json:"targets,omitempty"`
}

func statusCommand(opts globalOptions) error {
	report := gatherStatus(context.Background(), opts.port)
	if opts.json {
		return printJSON(report)
	}
	fmt.Printf("App running:       %v\n", report.AppRunning)
	fmt.Printf("Debug port %d:   %v\n", report.Port, report.DebugPortReady)
	if report.DebugPortReady {
		fmt.Printf("Browser:          %s\n", report.Browser)
		fmt.Printf("Protocol:         %s\n", report.ProtocolVersion)
		fmt.Printf("Page targets:     %d\n", report.PageTargets)
	} else {
		fmt.Printf("Hint:             run `zcodecli start`\n")
	}
	return nil
}

func startCommand(opts globalOptions) error {
	lockCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	lock, err := acquireControlLock(lockCtx)
	cancel()
	if err != nil {
		return err
	}
	defer releaseControlLock(lock)

	if report := gatherStatus(context.Background(), opts.port); report.DebugPortReady {
		if err := waitForBridge(opts.port, 5*time.Second); err != nil {
			return err
		}
		return statusCommand(opts)
	}
	if appRunning() {
		if err := quitApp(); err != nil {
			return fmt.Errorf("quit existing ZCode instance: %w", err)
		}
		if err := waitForAppExit(20 * time.Second); err != nil {
			return err
		}
	}

	cmd := exec.Command("open", "-a", "ZCode", "--args",
		fmt.Sprintf("--remote-debugging-port=%d", opts.port))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start ZCode with debug port: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := waitForDebugPort(opts.port, 30*time.Second); err != nil {
		return err
	}
	if err := waitForBridge(opts.port, 20*time.Second); err != nil {
		return err
	}
	return statusCommand(opts)
}

func stopCommand(opts globalOptions) error {
	lockCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	lock, err := acquireControlLock(lockCtx)
	cancel()
	if err != nil {
		return err
	}
	defer releaseControlLock(lock)

	if !appRunning() {
		if opts.json {
			return printJSON(map[string]any{"stopped": true, "wasRunning": false})
		}
		fmt.Println("ZCode is not running.")
		return nil
	}
	if err := quitApp(); err != nil {
		return err
	}
	if err := waitForAppExit(20 * time.Second); err != nil {
		return err
	}
	if opts.json {
		return printJSON(map[string]any{"stopped": true, "wasRunning": true})
	}
	fmt.Println("ZCode stopped.")
	return nil
}

func gatherStatus(ctx context.Context, port int) statusReport {
	report := statusReport{Port: port, AppRunning: appRunning()}
	if !debugPortReady(port, time.Second) {
		return report
	}
	report.DebugPortReady = true

	versionCtx, cancelVersion := context.WithTimeout(ctx, 3*time.Second)
	if info, err := fetchVersion(versionCtx, port); err == nil {
		report.Browser = info.Browser
		report.ProtocolVersion = info.ProtocolVersion
		report.UserAgent = info.UserAgent
		report.WebsocketURL = info.WebSocketDebuggerURL
	}
	cancelVersion()

	targetCtx, cancelTargets := context.WithTimeout(ctx, 3*time.Second)
	defer cancelTargets()
	req, err := http.NewRequestWithContext(targetCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json", port), nil)
	if err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			var targets []targetInfo
			if json.NewDecoder(resp.Body).Decode(&targets) == nil {
				for _, target := range targets {
					if target.Type == "page" {
						report.PageTargets++
					}
				}
				report.Targets = targets
			}
			_ = resp.Body.Close()
		}
	}
	return report
}

func appRunning() bool {
	return exec.Command("pgrep", "-x", "ZCode").Run() == nil
}

func quitApp() error {
	out, err := exec.Command("osascript", "-e", `quit app "ZCode"`).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func waitForAppExit(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !appRunning() {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for ZCode to exit; refusing to force-kill it")
}

func waitForDebugPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if debugPortReady(port, time.Second) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for ZCode debug port %d", port)
}

func debugPortReady(port int, timeout time.Duration) bool {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForBridge(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		session, err := openBridgeSessionLocked(ctx, port)
		cancel()
		if err == nil {
			return session.Close()
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("debug port is open but window.zcode is unavailable: %w", lastErr)
}
