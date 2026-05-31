package browser_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// BrowserAgent provides high-level browser automation via CDP.
type BrowserAgent struct {
	client *CDPClient
	pages  map[string]*Page
}

// New creates a new BrowserAgent connected to a Chrome/Chromium instance.
// debugURL should be like "http://localhost:9222".
func New(debugURL string) (*BrowserAgent, error) {
	client, err := NewCDPClient(debugURL)
	if err != nil {
		return nil, fmt.Errorf("connect browser: %w", err)
	}
	return &BrowserAgent{
		client: client,
		pages:  make(map[string]*Page),
	}, nil
}

// NewPage opens a new tab and returns a Page handle.
func (ba *BrowserAgent) NewPage(ctx context.Context) (*Page, error) {
	if ba.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	result, err := ba.client.SendCommand(ctx, "Target.createTarget", map[string]interface{}{
		"url": "about:blank",
	})
	if err != nil {
		return nil, err
	}
	targetID := result["targetId"].(string)
	page := &Page{client: ba.client, targetID: targetID}
	ba.pages[targetID] = page
	return page, nil
}

// Close shuts down the browser connection.
func (ba *BrowserAgent) Close() error {
	if ba.client == nil {
		return fmt.Errorf("client not initialized")
	}
	return ba.client.Close()
}

// Page represents a single browser tab.
type Page struct {
	client   *CDPClient
	targetID string
	url      string
	title    string
}

// Navigate loads a URL in this page.
func (p *Page) Navigate(ctx context.Context, url string) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	_, err := p.client.SendCommand(ctx, "Page.navigate", map[string]interface{}{
		"url": url,
	})
	if err != nil {
		return err
	}
	p.url = url
	return nil
}

// Snapshot returns a list of interactive elements on the page.
func (p *Page) Snapshot(ctx context.Context) (*PageSnapshot, error) {
	if p.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	// Inject element IDs via JavaScript
	script := `
		(() => {
			const elements = document.querySelectorAll(
				'a, button, input, select, textarea, [role="button"], [onclick]'
			);
			let id = 0;
			const results = [];
			elements.forEach(el => {
				el.setAttribute('data-ba-id', String(++id));
				const rect = el.getBoundingClientRect();
				results.push({
					id: String(id),
					tag: el.tagName.toLowerCase(),
					type: el.type || '',
					text: (el.textContent || '').trim().substring(0, 200),
					placeholder: el.placeholder || '',
					name: el.name || '',
					href: el.href || '',
					visible: rect.width > 0 && rect.height > 0
				});
			});
			return JSON.stringify(results);
		})()
	`

	result, err := p.client.Evaluate(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	var elements []*DOMElement
	if err := json.Unmarshal([]byte(result), &elements); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}

	return &PageSnapshot{
		URL:      p.url,
		Title:    p.title,
		Elements: elements,
	}, nil
}

// Click clicks an element by its data-ba-id.
func (p *Page) Click(ctx context.Context, elementID string) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	script := fmt.Sprintf(`
		(() => {
			const el = document.querySelector('[data-ba-id="%s"]');
			if (!el) return JSON.stringify({error: "element not found"});
			el.scrollIntoView({block: "center"});
			el.click();
			return JSON.stringify({ok: true});
		})()
	`, elementID)
	_, err := p.client.Evaluate(ctx, script)
	return err
}

// Type enters text into an input element.
func (p *Page) Type(ctx context.Context, elementID, text string) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	script := fmt.Sprintf(`
		(() => {
			const el = document.querySelector('[data-ba-id="%s"]');
			if (!el) return JSON.stringify({error: "element not found"});
			el.focus();
			el.value = %q;
			el.dispatchEvent(new Event('input', {bubbles: true}));
			return JSON.stringify({ok: true, value: el.value});
		})()
	`, elementID, text)
	_, err := p.client.Evaluate(ctx, script)
	return err
}

// Scroll scrolls the page.
func (p *Page) Scroll(ctx context.Context, direction string, amount int) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	script := fmt.Sprintf(`window.scrollBy(0, %d)`, amount)
	if direction == "up" {
		script = fmt.Sprintf(`window.scrollBy(0, %d)`, -amount)
	}
	_, err := p.client.Evaluate(ctx, script)
	return err
}

// WaitFor waits for text to appear on the page.
func (p *Page) WaitFor(ctx context.Context, text string, timeout time.Duration) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	deadline := time.Now().Add(timeout)
	script := fmt.Sprintf(`document.body.innerText.includes(%q)`, text)
	for time.Now().Before(deadline) {
		result, _ := p.client.Evaluate(ctx, script)
		if result == "true" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for text: %s", text)
}

// PageSnapshot describes the current state of a page.
type PageSnapshot struct {
	URL      string        `json:"url"`
	Title    string        `json:"title"`
	Elements []*DOMElement `json:"elements"`
}

// DOMElement represents an interactive element.
type DOMElement struct {
	ID          string `json:"id"`
	Tag         string `json:"tag"`
	Type        string `json:"type"`
	Text        string `json:"text"`
	Placeholder string `json:"placeholder"`
	Name        string `json:"name"`
	Href        string `json:"href"`
	Visible     bool   `json:"visible"`
}

// ExecuteAutomation runs a multi-step browser automation task.
// task is a natural language description of what to do.
// planner is a function that, given a snapshot, returns the next action.
func (ba *BrowserAgent) ExecuteAutomation(
	ctx context.Context,
	page *Page,
	task string,
	planner func(snapshot *PageSnapshot, step int) (*Action, error),
	maxSteps int,
) (*AutomationResult, error) {
	for step := 0; step < maxSteps; step++ {
		snapshot, err := page.Snapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("step %d snapshot: %w", step, err)
		}

		action, err := planner(snapshot, step)
		if err != nil {
			return nil, fmt.Errorf("step %d plan: %w", step, err)
		}

		switch action.Type {
		case "click":
			if err := page.Click(ctx, action.ElementID); err != nil {
				return nil, fmt.Errorf("step %d click: %w", step, err)
			}
		case "type":
			if err := page.Type(ctx, action.ElementID, action.Text); err != nil {
				return nil, fmt.Errorf("step %d type: %w", step, err)
			}
		case "navigate":
			if err := page.Navigate(ctx, action.URL); err != nil {
				return nil, fmt.Errorf("step %d navigate: %w", step, err)
			}
		case "scroll":
			if err := page.Scroll(ctx, action.Direction, action.Amount); err != nil {
				return nil, fmt.Errorf("step %d scroll: %w", step, err)
			}
		case "wait":
			timeout := action.Timeout
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			if err := page.WaitFor(ctx, action.Text, timeout); err != nil {
				return nil, fmt.Errorf("step %d wait: %w", step, err)
			}
		case "done":
			return &AutomationResult{Success: true, Steps: step + 1, Summary: action.Summary}, nil
		case "fail":
			return &AutomationResult{Success: false, Steps: step + 1, Error: action.Error}, nil
		}

		time.Sleep(500 * time.Millisecond) // let page settle
	}

	return nil, fmt.Errorf("exceeded max steps (%d)", maxSteps)
}

// Action describes a single browser automation step.
type Action struct {
	Type      string        `json:"type"` // click, type, navigate, scroll, wait, done, fail
	ElementID string        `json:"element_id,omitempty"`
	Text      string        `json:"text,omitempty"`
	URL       string        `json:"url,omitempty"`
	Direction string        `json:"direction,omitempty"` // up, down
	Amount    int           `json:"amount,omitempty"`
	Timeout   time.Duration `json:"timeout,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// AutomationResult summarizes a completed automation task.
type AutomationResult struct {
	Success bool   `json:"success"`
	Steps   int    `json:"steps"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Close closes this page.
func (p *Page) Close(ctx context.Context) error {
	if p.client == nil {
		return fmt.Errorf("client not initialized")
	}
	_, err := p.client.SendCommand(ctx, "Target.closeTarget", map[string]interface{}{
		"targetId": p.targetID,
	})
	return err
}

// GetTitle returns the page title.
func (p *Page) GetTitle(ctx context.Context) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("client not initialized")
	}
	result, err := p.client.Evaluate(ctx, "document.title")
	if err != nil {
		return "", err
	}
	p.title = result
	return result, nil
}

// GetText extracts visible text from the page.
func (p *Page) GetText(ctx context.Context) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("client not initialized")
	}
	return p.client.Evaluate(ctx, "document.body.innerText")
}

// ExtractJSON runs a querySelector and returns matching elements as JSON.
func (p *Page) ExtractJSON(ctx context.Context, selector string) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("client not initialized")
	}
	script := fmt.Sprintf(`
		(() => {
			const els = document.querySelectorAll(%q);
			return JSON.stringify(Array.from(els).map(el => ({
				tag: el.tagName,
				text: (el.textContent || '').trim().substring(0, 500),
				href: el.href || '',
				src: el.src || ''
			})));
		})()
	`, selector)
	return p.client.Evaluate(ctx, script)
}

func init() {
	slog.Debug("browser_agent: package initialized")
}
