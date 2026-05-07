package fleetnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type nativeBrowserState struct {
	mu             sync.Mutex
	allocCtx       context.Context
	cancel         context.CancelFunc
	browserCtx     context.Context
	browserCancel  context.CancelFunc
	executablePath string
	headless       bool
	tabs           map[string]*nativeBrowserTab
	activeTargetID string
}

type nativeBrowserTab struct {
	ctx    context.Context
	cancel context.CancelFunc
	refs   map[string]string
}

var nativeBrowser = &nativeBrowserState{}

func browserProxyAvailable(cfg Config) bool {
	if strings.TrimSpace(cfg.BrowserProxyURL) != "" {
		return true
	}
	_, err := resolveBrowserExecutablePath(cfg)
	return err == nil
}

func browserProxy(ctx context.Context, cfg Config, params map[string]any, timeout time.Duration) (any, error) {
	if strings.TrimSpace(cfg.BrowserProxyURL) != "" {
		return httpBrowserProxy(ctx, cfg, params, timeout)
	}
	return nativeBrowserProxy(ctx, cfg, params, timeout)
}

func httpBrowserProxy(ctx context.Context, cfg Config, params map[string]any, timeout time.Duration) (any, error) {
	method, _ := params["method"].(string)
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	path, _ := params["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("browser proxy path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	targetURL, err := url.Parse(cfg.BrowserProxyURL + path)
	if err != nil {
		return nil, err
	}
	query := targetURL.Query()
	for key, value := range mapValue(params["query"]) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				query.Add(key, fmt.Sprint(item))
			}
		default:
			query.Set(key, fmt.Sprint(typed))
		}
	}
	targetURL.RawQuery = query.Encode()

	var body io.Reader
	if rawBody, ok := params["body"]; ok && rawBody != nil {
		payload, err := json.Marshal(rawBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	requestCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, method, targetURL.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("browser proxy returned %s: %s", res.Status, strings.TrimSpace(string(raw)))
	}
	if len(raw) == 0 {
		return map[string]any{"result": map[string]any{"status": res.StatusCode}}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return map[string]any{"result": decoded}, nil
	}
	return map[string]any{"result": map[string]any{"status": res.StatusCode, "body": string(raw)}}, nil
}

func nativeBrowserProxy(ctx context.Context, cfg Config, params map[string]any, timeout time.Duration) (any, error) {
	requestCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	st, err := getNativeBrowser(requestCtx, cfg)
	if err != nil {
		return nil, err
	}
	method, _ := params["method"].(string)
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	path, _ := params["path"].(string)
	path = normalizeBrowserPath(path)
	query := mapValue(params["query"])
	body := mapValue(params["body"])

	var result any
	switch {
	case method == http.MethodGet && path == "/":
		result = map[string]any{"running": true, "transport": "chromedp", "cdpReady": true}
	case method == http.MethodGet && path == "/tabs":
		result, err = st.listTabs(requestCtx)
	case method == http.MethodPost && path == "/tabs/open":
		result, err = st.openTab(requestCtx, stringFromMaps("url", body, query))
	case method == http.MethodPost && path == "/navigate":
		result, err = st.navigate(requestCtx, stringFromMaps("targetId", body, query), stringFromMaps("url", body, query))
	case method == http.MethodGet && path == "/snapshot":
		result, err = st.snapshot(requestCtx, stringFromMaps("targetId", query, body))
	case method == http.MethodPost && path == "/act":
		result, err = st.act(requestCtx, body, query)
	case method == http.MethodGet && path == "/screenshot":
		result, err = st.screenshot(requestCtx, stringFromMaps("targetId", query, body))
	default:
		err = fmt.Errorf("unsupported browser proxy route %s %s", method, path)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": result}, nil
}

func getNativeBrowser(ctx context.Context, cfg Config) (*nativeBrowserState, error) {
	executablePath, err := resolveBrowserExecutablePath(cfg)
	if err != nil {
		return nil, err
	}
	nativeBrowser.mu.Lock()
	defer nativeBrowser.mu.Unlock()
	if nativeBrowser.browserCtx != nil && nativeBrowser.executablePath == executablePath && nativeBrowser.headless == cfg.BrowserHeadless {
		return nativeBrowser, nil
	}
	if nativeBrowser.cancel != nil {
		nativeBrowser.cancel()
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(executablePath),
		chromedp.UserDataDir(defaultBrowserUserDataDir()),
	)
	if cfg.BrowserHeadless {
		options = append(options, chromedp.Headless)
	} else {
		options = append(options,
			chromedp.Flag("headless", false),
			chromedp.Flag("hide-scrollbars", false),
			chromedp.Flag("mute-audio", false),
		)
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	if err := ctx.Err(); err != nil {
		browserCancel()
		allocCancel()
		return nil, err
	}
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return nil, err
	}
	nativeBrowser.allocCtx = allocCtx
	nativeBrowser.cancel = allocCancel
	nativeBrowser.browserCtx = browserCtx
	nativeBrowser.browserCancel = browserCancel
	nativeBrowser.executablePath = executablePath
	nativeBrowser.headless = cfg.BrowserHeadless
	nativeBrowser.tabs = map[string]*nativeBrowserTab{}
	nativeBrowser.activeTargetID = ""
	return nativeBrowser, nil
}

func (st *nativeBrowserState) listTabs(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets, err := chromedp.Targets(st.browserCtx)
	if err != nil {
		return nil, err
	}
	tabs := make([]map[string]any, 0, len(targets))
	for _, info := range targets {
		if info.Type != "page" {
			continue
		}
		tabs = append(tabs, map[string]any{
			"targetId": string(info.TargetID),
			"title":    info.Title,
			"url":      info.URL,
			"type":     info.Type,
			"active":   string(info.TargetID) == st.activeTargetID,
		})
	}
	return map[string]any{"tabs": tabs}, nil
}

func (st *nativeBrowserState) openTab(ctx context.Context, pageURL string) (map[string]any, error) {
	if strings.TrimSpace(pageURL) == "" {
		return nil, errors.New("url is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tabCtx, cancel := chromedp.NewContext(st.browserCtx)
	if _, err := chromedp.RunResponse(tabCtx, chromedp.Navigate(pageURL)); err != nil {
		cancel()
		return nil, err
	}
	targetID := string(chromedp.FromContext(tabCtx).Target.TargetID)
	st.mu.Lock()
	st.tabs[targetID] = &nativeBrowserTab{ctx: tabCtx, cancel: cancel, refs: map[string]string{}}
	st.activeTargetID = targetID
	st.mu.Unlock()
	return map[string]any{"targetId": targetID, "url": pageURL}, nil
}

func (st *nativeBrowserState) navigate(ctx context.Context, targetID, pageURL string) (map[string]any, error) {
	if strings.TrimSpace(pageURL) == "" {
		return nil, errors.New("url is required")
	}
	tab, targetID, err := st.tab(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := chromedp.RunResponse(tab.ctx, chromedp.Navigate(pageURL)); err != nil {
		return nil, err
	}
	st.mu.Lock()
	tab.refs = map[string]string{}
	st.activeTargetID = targetID
	st.mu.Unlock()
	return map[string]any{"targetId": targetID, "url": pageURL}, nil
}

func (st *nativeBrowserState) snapshot(ctx context.Context, targetID string) (map[string]any, error) {
	tab, targetID, err := st.tab(ctx, targetID)
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Items []struct {
			Ref      string         `json:"ref"`
			Role     string         `json:"role"`
			Name     string         `json:"name"`
			Text     string         `json:"text"`
			Tag      string         `json:"tag"`
			Selector string         `json:"selector"`
			Bounds   map[string]any `json:"bounds"`
			Disabled bool           `json:"disabled"`
		} `json:"items"`
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := chromedp.Run(tab.ctx, chromedp.Evaluate(snapshotScript, &snapshot)); err != nil {
		return nil, err
	}
	refs := make(map[string]string, len(snapshot.Items))
	nodes := make([]map[string]any, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		ref := normalizeBrowserRef(item.Ref)
		if ref == "" || item.Selector == "" {
			continue
		}
		refs[ref] = item.Selector
		nodes = append(nodes, map[string]any{
			"ref":      ref,
			"role":     item.Role,
			"name":     item.Name,
			"text":     item.Text,
			"tag":      item.Tag,
			"bounds":   item.Bounds,
			"disabled": item.Disabled,
		})
	}
	st.mu.Lock()
	tab.refs = refs
	st.activeTargetID = targetID
	st.mu.Unlock()
	return map[string]any{
		"targetId": targetID,
		"title":    snapshot.Title,
		"url":      snapshot.URL,
		"nodes":    nodes,
	}, nil
}

func (st *nativeBrowserState) act(ctx context.Context, body, query map[string]any) (map[string]any, error) {
	targetID := stringFromMaps("targetId", body, query)
	tab, targetID, err := st.tab(ctx, targetID)
	if err != nil {
		return nil, err
	}
	kind := firstNonEmpty(stringFromMaps("kind", body, query), stringFromMaps("action", body, query))
	if kind == "" {
		return nil, errors.New("act kind is required")
	}
	selector := strings.TrimSpace(stringFromMaps("selector", body, query))
	if selector == "" {
		ref := normalizeBrowserRef(stringFromMaps("ref", body, query))
		st.mu.Lock()
		selector = tab.refs[ref]
		st.mu.Unlock()
	}
	if selector == "" && kind != "press" {
		return nil, errors.New("selector or fresh snapshot ref is required")
	}
	var actions []chromedp.Action
	switch kind {
	case "click":
		actions = append(actions, chromedp.Click(selector, chromedp.ByQuery))
	case "type", "input":
		text := firstNonEmpty(stringFromMaps("text", body, query), stringFromMaps("value", body, query))
		actions = append(actions, chromedp.SendKeys(selector, text, chromedp.ByQuery))
	case "fill":
		text := firstNonEmpty(stringFromMaps("text", body, query), stringFromMaps("value", body, query))
		actions = append(actions, chromedp.SetValue(selector, text, chromedp.ByQuery))
	case "press":
		key := firstNonEmpty(stringFromMaps("key", body, query), stringFromMaps("text", body, query))
		if key == "" {
			return nil, errors.New("key is required")
		}
		if selector != "" {
			actions = append(actions, chromedp.Focus(selector, chromedp.ByQuery))
		}
		actions = append(actions, chromedp.KeyEvent(key))
	default:
		return nil, fmt.Errorf("unsupported act kind %q", kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := chromedp.Run(tab.ctx, actions...); err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.activeTargetID = targetID
	st.mu.Unlock()
	return map[string]any{"targetId": targetID, "ok": true}, nil
}

func (st *nativeBrowserState) screenshot(ctx context.Context, targetID string) (map[string]any, error) {
	tab, targetID, err := st.tab(ctx, targetID)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := chromedp.Run(tab.ctx, chromedp.FullScreenshot(&raw, 90)); err != nil {
		return nil, err
	}
	return map[string]any{
		"targetId":    targetID,
		"mimeType":    "image/png",
		"base64":      base64.StdEncoding.EncodeToString(raw),
		"sizeInBytes": len(raw),
	}, nil
}

func (st *nativeBrowserState) tab(ctx context.Context, targetID string) (*nativeBrowserTab, string, error) {
	st.mu.Lock()
	if strings.TrimSpace(targetID) == "" {
		targetID = st.activeTargetID
	}
	if targetID != "" {
		if tab := st.tabs[targetID]; tab != nil {
			st.mu.Unlock()
			return tab, targetID, nil
		}
	}
	st.mu.Unlock()
	if targetID == "" {
		tabs, err := st.listTabs(ctx)
		if err != nil {
			return nil, "", err
		}
		items, _ := tabs["tabs"].([]map[string]any)
		if len(items) == 0 {
			opened, err := st.openTab(ctx, "about:blank")
			if err != nil {
				return nil, "", err
			}
			targetID, _ = opened["targetId"].(string)
		} else {
			targetID, _ = items[0]["targetId"].(string)
		}
	}
	tabCtx, cancel := chromedp.NewContext(st.browserCtx, chromedp.WithTargetID(target.ID(targetID)))
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, "", err
	}
	if err := chromedp.Run(tabCtx); err != nil {
		cancel()
		return nil, "", err
	}
	tab := &nativeBrowserTab{ctx: tabCtx, cancel: cancel, refs: map[string]string{}}
	st.mu.Lock()
	st.tabs[targetID] = tab
	st.activeTargetID = targetID
	st.mu.Unlock()
	return tab, targetID, nil
}

func resolveBrowserExecutablePath(cfg Config) (string, error) {
	for _, candidate := range []string{cfg.BrowserExecutablePath, os.Getenv("FLEETN_BROWSER_EXECUTABLE_PATH")} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
		return "", fmt.Errorf("browser executable not found: %s", candidate)
	}
	for _, candidate := range browserExecutableCandidates() {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Chrome/Chromium executable not found; set FLEETN_BROWSER_EXECUTABLE_PATH or FLEETN_BROWSER_PROXY_URL")
}

func browserExecutableCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"chromium",
			"google-chrome",
			"chrome",
		}
	case "windows":
		return []string{
			"chrome.exe",
			"msedge.exe",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			filepath.Join(os.Getenv("USERPROFILE"), `AppData\Local\Google\Chrome\Application\chrome.exe`),
		}
	default:
		return []string{
			"chromium",
			"chromium-browser",
			"google-chrome",
			"google-chrome-stable",
			"headless_shell",
			"headless-shell",
			"chrome",
			"/usr/bin/google-chrome",
			"/snap/bin/chromium",
		}
	}
}

func defaultBrowserUserDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "fleetn", "browser-profile")
	}
	return filepath.Join(home, ".fleetn", "browser-profile")
}

func normalizeBrowserPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func normalizeBrowserRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "e")
	ref = strings.TrimPrefix(ref, "#")
	if ref == "" {
		return ""
	}
	return "e" + ref
}

func stringFromMaps(key string, maps ...map[string]any) string {
	for _, values := range maps {
		if values == nil {
			continue
		}
		if value, ok := values[key]; ok && value != nil {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func durationFromAny(value any) time.Duration {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return time.Duration(typed) * time.Millisecond
		}
	case int64:
		if typed > 0 {
			return time.Duration(typed) * time.Millisecond
		}
	case float64:
		if typed > 0 && !math.IsNaN(typed) && !math.IsInf(typed, 0) {
			return time.Duration(typed) * time.Millisecond
		}
	case json.Number:
		if value, err := typed.Int64(); err == nil && value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case string:
		if value, err := time.ParseDuration(strings.TrimSpace(typed)); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

const snapshotScript = `(() => {
  function textOf(el) {
    return ((el.innerText || el.value || el.getAttribute("aria-label") || el.title || "").trim()).slice(0, 300);
  }
  function cssPath(el) {
    if (el.id) return "#" + CSS.escape(el.id);
    const parts = [];
    while (el && el.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
      let part = el.localName.toLowerCase();
      if (el.classList && el.classList.length) part += "." + Array.from(el.classList).slice(0, 2).map(CSS.escape).join(".");
      const parent = el.parentElement;
      if (parent) {
        const siblings = Array.from(parent.children).filter(x => x.localName === el.localName);
        if (siblings.length > 1) part += ":nth-of-type(" + (siblings.indexOf(el) + 1) + ")";
      }
      parts.unshift(part);
      el = parent;
    }
    return parts.join(" > ");
  }
  const selector = "a,button,input,textarea,select,[role],[onclick],[tabindex]";
  const items = Array.from(document.querySelectorAll(selector)).slice(0, 300).map((el, idx) => {
    const rect = el.getBoundingClientRect();
    return {
      ref: "e" + (idx + 1),
      role: el.getAttribute("role") || "",
      name: el.getAttribute("aria-label") || el.getAttribute("name") || "",
      text: textOf(el),
      tag: el.localName.toLowerCase(),
      selector: cssPath(el),
      bounds: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      disabled: !!el.disabled || el.getAttribute("aria-disabled") === "true"
    };
  });
  return { title: document.title, url: location.href, items };
})()`
