// Read-only command surface over the frozen window.zcode bridge: per-command
// contract validation, contract-mismatch errors, and the shared table/JSON
// printers.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

type sshEntry struct {
	Alias          string  `json:"alias"`
	Host           string  `json:"host"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	PrivateKeyPath *string `json:"privateKeyPath"`
	Source         string  `json:"source"`
}

type editorEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IconDataURL string `json:"iconDataUrl"`
}

type bridgeMethod struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Arity        *int   `json:"arity"`
	Writable     bool   `json:"writable"`
	Configurable bool   `json:"configurable"`
	Enumerable   bool   `json:"enumerable"`
}

type autoUpdatePreferences struct {
	AutoDownloadAndInstallUpdates bool `json:"autoDownloadAndInstallUpdates"`
}

type updateState struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"`
}

type desktopActivity struct {
	RunningAgentSessionCount int `json:"runningAgentSessionCount"`
	RunningRepoWikiTaskCount int `json:"runningRepoWikiTaskCount"`
}

type windowChromeState struct {
	IsMaximized                  bool `json:"isMaximized"`
	SupportsNativeRoundedCorners bool `json:"supportsNativeRoundedCorners"`
}

type zoomLevel struct {
	ZoomLevel float64 `json:"zoomLevel"`
}

type overlayMetrics struct {
	LeftPaddingPx float64 `json:"leftPaddingPx"`
}

type remoteStatus struct {
	Status string `json:"status"`
}

func sshListCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "listSSHConfigAliases")
	if err != nil {
		return err
	}
	entries, err := validateSSHList(raw)
	if err != nil {
		return err
	}
	if opts.json {
		return printRawJSON(raw)
	}
	printSSHTable(entries)
	return nil
}

func systemCommand(action string, opts globalOptions) error {
	switch action {
	case "locale":
		raw, err := callReadMethod(opts, "getSystemLocale")
		if err != nil {
			return err
		}
		var value string
		if err := decodeRequiredString(raw, &value, "locale"); err != nil {
			return err
		}
		if opts.json {
			return printRawJSON(raw)
		}
		printKeyValueTable([][2]string{{"LOCALE", value}})
	case "device-id":
		raw, err := callReadMethod(opts, "getDeviceId")
		if err != nil {
			return err
		}
		var value string
		if err := decodeRequiredString(raw, &value, "deviceId"); err != nil {
			return err
		}
		if opts.json {
			return printRawJSON(raw)
		}
		printKeyValueTable([][2]string{{"DEVICE ID", value}})
	default:
		return fmt.Errorf("unknown system action %q", action)
	}
	return nil
}

func editorsListCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "getInstalledEditors")
	if err != nil {
		return err
	}
	var editors []editorEntry
	if err := json.Unmarshal(raw, &editors); err != nil {
		return contractMismatch("editors list: expected an array: %w", err)
	}
	for i, editor := range editors {
		if editor.ID == "" || editor.Name == "" || editor.IconDataURL == "" {
			return contractMismatch("editors list: entry %d has an empty id, name, or iconDataUrl", i)
		}
	}
	if opts.json {
		return printRawJSON(raw)
	}
	if len(editors) == 0 {
		fmt.Println("No installed editors found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME")
	for _, editor := range editors {
		fmt.Fprintf(w, "%s\t%s\n", editor.ID, editor.Name)
	}
	_ = w.Flush()
	return nil
}

func dockerCommand(action string, opts globalOptions) error {
	switch action {
	case "status":
		raw, err := callReadMethod(opts, "isDockerAvailable")
		if err != nil {
			return err
		}
		var available bool
		if err := json.Unmarshal(raw, &available); err != nil {
			return contractMismatch("docker status: expected boolean: %w", err)
		}
		if opts.json {
			return printJSON(map[string]bool{"available": available})
		}
		printKeyValueTable([][2]string{{"AVAILABLE", fmt.Sprintf("%t", available)}})
	case "containers":
		raw, err := callReadMethod(opts, "listDockerContainers")
		if err != nil {
			return err
		}
		if err := validateArray(raw, "docker containers"); err != nil {
			return err
		}
		if opts.json {
			return printRawJSON(raw)
		}
		printObjectArrayTable(raw, "No Docker containers found.")
	default:
		return fmt.Errorf("unknown docker action %q", action)
	}
	return nil
}

func wslListCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "listWSLDistros")
	if err != nil {
		return err
	}
	if err := validateArray(raw, "wsl distros"); err != nil {
		return err
	}
	if opts.json {
		return printRawJSON(raw)
	}
	printObjectArrayTable(raw, "No WSL distros found.")
	return nil
}

func updatesStatusCommand(opts globalOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := openBridgeSession(ctx, opts.port)
	if err != nil {
		return err
	}
	defer session.Close()

	preferencesRaw, err := session.callZCode("getAutoUpdatePreferences")
	if err != nil {
		return err
	}
	stateRaw, err := session.callZCode("getUpdateState")
	if err != nil {
		return err
	}
	var preferences autoUpdatePreferences
	var state updateState
	if err := json.Unmarshal(preferencesRaw, &preferences); err != nil {
		return contractMismatch("update preferences: %w", err)
	}
	if err := json.Unmarshal(stateRaw, &state); err != nil || state.Kind == "" {
		return contractMismatch("update state: expected an object with non-empty kind")
	}
	status := map[string]any{
		"enabled":                       state.Enabled,
		"kind":                          state.Kind,
		"autoDownloadAndInstallUpdates": preferences.AutoDownloadAndInstallUpdates,
	}
	if opts.json {
		return printJSON(status)
	}
	printKeyValueTable([][2]string{
		{"ENABLED", fmt.Sprintf("%t", state.Enabled)},
		{"KIND", state.Kind},
		{"AUTO DOWNLOAD AND INSTALL", fmt.Sprintf("%t", preferences.AutoDownloadAndInstallUpdates)},
	})
	return nil
}

func windowStateCommand(opts globalOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := openBridgeSession(ctx, opts.port)
	if err != nil {
		return err
	}
	defer session.Close()

	chromeRaw, err := session.callZCode("getDesktopWindowChromeState")
	if err != nil {
		return err
	}
	zoomRaw, err := session.callZCode("getDesktopZoomLevel")
	if err != nil {
		return err
	}
	overlayRaw, err := session.callZCode("getWindowControlsOverlayMetrics")
	if err != nil {
		return err
	}
	var chrome windowChromeState
	var zoom zoomLevel
	var overlay overlayMetrics
	if err := json.Unmarshal(chromeRaw, &chrome); err != nil {
		return contractMismatch("window chrome state: %w", err)
	}
	if err := json.Unmarshal(zoomRaw, &zoom); err != nil {
		return contractMismatch("zoom level: %w", err)
	}
	if err := json.Unmarshal(overlayRaw, &overlay); err != nil || overlay.LeftPaddingPx < 0 {
		return contractMismatch("window overlay metrics")
	}
	state := map[string]any{
		"maximized":                    chrome.IsMaximized,
		"supportsNativeRoundedCorners": chrome.SupportsNativeRoundedCorners,
		"zoomLevel":                    zoom.ZoomLevel,
		"leftPaddingPx":                overlay.LeftPaddingPx,
	}
	if opts.json {
		return printJSON(state)
	}
	printKeyValueTable([][2]string{
		{"MAXIMIZED", fmt.Sprintf("%t", chrome.IsMaximized)},
		{"NATIVE ROUNDED CORNERS", fmt.Sprintf("%t", chrome.SupportsNativeRoundedCorners)},
		{"ZOOM LEVEL", fmt.Sprintf("%g", zoom.ZoomLevel)},
		{"LEFT PADDING PX", fmt.Sprintf("%g", overlay.LeftPaddingPx)},
	})
	return nil
}

func remoteStatusCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "getWebRemoteControlStatus")
	if err != nil {
		return err
	}
	var status remoteStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.Status == "" {
		return contractMismatch("remote status: expected an object with non-empty status")
	}
	if opts.json {
		return printRawJSON(raw)
	}
	printKeyValueTable([][2]string{{"STATUS", status.Status}})
	return nil
}

func desktopActivityCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "getDesktopSessionActivity")
	if err != nil {
		return err
	}
	var activity desktopActivity
	if err := json.Unmarshal(raw, &activity); err != nil {
		return contractMismatch("desktop activity: %w", err)
	}
	if activity.RunningAgentSessionCount < 0 || activity.RunningRepoWikiTaskCount < 0 {
		return contractMismatch("desktop activity: counts cannot be negative")
	}
	if opts.json {
		return printRawJSON(raw)
	}
	printKeyValueTable([][2]string{
		{"RUNNING AGENT SESSIONS", fmt.Sprintf("%d", activity.RunningAgentSessionCount)},
		{"RUNNING REPO WIKI TASKS", fmt.Sprintf("%d", activity.RunningRepoWikiTaskCount)},
	})
	return nil
}

func communityStatusCommand(opts globalOptions) error {
	raw, err := callReadMethod(opts, "canOpenCommunity")
	if err != nil {
		return err
	}
	var canOpen bool
	if err := json.Unmarshal(raw, &canOpen); err != nil {
		return contractMismatch("community status: expected boolean: %w", err)
	}
	if opts.json {
		return printJSON(map[string]bool{"canOpen": canOpen})
	}
	printKeyValueTable([][2]string{{"CAN OPEN", fmt.Sprintf("%t", canOpen)}})
	return nil
}

func exploreMethodsCommand(opts globalOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := openBridgeSession(ctx, opts.port)
	if err != nil {
		return err
	}
	defer session.Close()

	raw, err := session.evaluate(`(() => {
  if (!window.zcode) {
    const error = new Error('window.zcode is unavailable');
    error.name = 'MissingBridge';
    throw error;
  }
  return Object.getOwnPropertyNames(window.zcode)
    .filter(name => typeof window.zcode[name] === 'function')
    .map(name => {
      const descriptor = Object.getOwnPropertyDescriptor(window.zcode, name) || {};
      const value = window.zcode[name];
      return {
        name,
        type: typeof value,
        arity: typeof value === 'function' ? value.length : null,
        writable: Boolean(descriptor.writable),
        configurable: Boolean(descriptor.configurable),
        enumerable: Boolean(descriptor.enumerable)
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name));
})()`)
	if err != nil {
		return err
	}
	var methods []bridgeMethod
	if err := json.Unmarshal(raw, &methods); err != nil {
		return contractMismatch("bridge methods: %w", err)
	}
	if opts.json {
		return printRawJSON(raw)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tARITY\tWRITABLE\tCONFIGURABLE\tENUMERABLE")
	for _, method := range methods {
		arity := "-"
		if method.Arity != nil {
			arity = fmt.Sprintf("%d", *method.Arity)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%t\t%t\n", method.Name, method.Type, arity, method.Writable, method.Configurable, method.Enumerable)
	}
	_ = w.Flush()
	return nil
}

func validateSSHList(raw json.RawMessage) ([]sshEntry, error) {
	var entries []sshEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, contractMismatch("ssh list: expected an array: %w", err)
	}
	for i, entry := range entries {
		if entry.Alias == "" || entry.Host == "" || entry.Username == "" || entry.Source == "" {
			return nil, contractMismatch("ssh list: entry %d has an empty required string", i)
		}
		if entry.Port <= 0 || entry.Port > 65535 {
			return nil, contractMismatch("ssh list: entry %d has invalid port %d", i, entry.Port)
		}
		if entry.PrivateKeyPath != nil && *entry.PrivateKeyPath == "" {
			return nil, contractMismatch("ssh list: entry %d has empty privateKeyPath; expected null, omitted, or non-empty string", i)
		}
	}
	return entries, nil
}

func printSSHTable(entries []sshEntry) {
	if len(entries) == 0 {
		fmt.Println("No SSH aliases found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tHOST\tPORT\tUSERNAME\tPRIVATE KEY\tSOURCE")
	for _, entry := range entries {
		key := ""
		if entry.PrivateKeyPath != nil {
			key = *entry.PrivateKeyPath
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", entry.Alias, entry.Host, entry.Port, entry.Username, key, entry.Source)
	}
	_ = w.Flush()
}

func callReadMethod(opts globalOptions, method string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := openBridgeSession(ctx, opts.port)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.callZCode(method)
}

// contractMismatch reports a response that no longer matches the frozen contract.
func contractMismatch(format string, args ...any) error {
	return fmt.Errorf("contract mismatch: "+format+" (the application version may have changed)", args...)
}

func decodeRequiredString(raw json.RawMessage, target *string, field string) error {
	if err := json.Unmarshal(raw, target); err != nil || *target == "" {
		return contractMismatch("%s: expected a non-empty string", field)
	}
	return nil
}

func validateArray(raw json.RawMessage, label string) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return contractMismatch("%s: expected an array: %w", label, err)
	}
	return nil
}

func printObjectArrayTable(raw json.RawMessage, emptyText string) {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		fmt.Println(emptyText)
		return
	}
	columnSet := make(map[string]bool)
	for _, item := range items {
		for key := range item {
			columnSet[key] = true
		}
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i, column := range columns {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, strings.ToUpper(column))
	}
	fmt.Fprintln(w)
	for _, item := range items {
		for i, column := range columns {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, formatCell(item[column]))
		}
		fmt.Fprintln(w)
	}
	_ = w.Flush()
}

func formatCell(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(b)
}

func printKeyValueTable(rows [][2]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\n", row[0], row[1])
	}
	_ = w.Flush()
}

func printJSON(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func printRawJSON(raw json.RawMessage) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return err
	}
	fmt.Println(compact.String())
	return nil
}
