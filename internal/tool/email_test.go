package tool

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"
)

func TestSendEmailToolExecuteSendsEmail(t *testing.T) {
	tool := NewSendEmailTool("smtp.example.com", 587, "agent@example.com", "password", "from@example.com", false)
	var calls int
	var gotAddr string
	var gotFrom string
	var gotTo []string
	var gotMsg []byte
	tool.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		calls++
		gotAddr = addr
		gotFrom = from
		gotTo = to
		gotMsg = msg
		return nil
	}

	res, err := tool.Execute(context.Background(), []byte(`{"to":"user@example.com","subject":"Hello","body":"Body text"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Execute() result error = %q", res.Error)
	}
	if res.Output != "Email sent to user@example.com" {
		t.Fatalf("Output = %q, want Email sent to user@example.com", res.Output)
	}
	if calls != 1 {
		t.Fatalf("send calls = %d, want 1", calls)
	}
	if gotAddr != "smtp.example.com:587" {
		t.Fatalf("addr = %q, want smtp.example.com:587", gotAddr)
	}
	if gotFrom != "from@example.com" {
		t.Fatalf("from = %q, want from@example.com", gotFrom)
	}
	if len(gotTo) != 1 || gotTo[0] != "user@example.com" {
		t.Fatalf("to = %#v, want [user@example.com]", gotTo)
	}
	msg := string(gotMsg)
	if !strings.Contains(msg, "Subject: Hello") {
		t.Fatalf("message missing subject: %q", msg)
	}
	if !strings.Contains(msg, "Body text") {
		t.Fatalf("message missing body: %q", msg)
	}
}

func TestSendEmailToolExecuteInvalidJSON(t *testing.T) {
	tool := NewSendEmailTool("smtp.example.com", 587, "agent@example.com", "password", "", false)
	tool.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		t.Fatal("send should not be called")
		return nil
	}

	res, err := tool.Execute(context.Background(), []byte(`{`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasPrefix(res.Error, "invalid input: ") {
		t.Fatalf("Error = %q, want invalid input", res.Error)
	}
}

func TestSendEmailToolExecuteInvalidTo(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "missing to",
			input: `{"subject":"Hello","body":"Body text"}`,
		},
		{
			name:  "blank to",
			input: `{"to":"   ","subject":"Hello","body":"Body text"}`,
		},
		{
			name:  "missing at",
			input: `{"to":"user.example.com","subject":"Hello","body":"Body text"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewSendEmailTool("smtp.example.com", 587, "agent@example.com", "password", "", false)
			tool.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
				t.Fatal("send should not be called")
				return nil
			}

			res, err := tool.Execute(context.Background(), []byte(tc.input))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if res.Error == "" {
				t.Fatal("Error is empty, want validation error")
			}
		})
	}
}

func TestSendEmailToolExecuteRejectsHeaderInjection(t *testing.T) {
	cases := map[string]string{
		"crlf in subject": `{"to":"user@example.com","subject":"Hi\r\nBcc: evil@x.com","body":"Body"}`,
		"crlf in to":      `{"to":"user@example.com\r\nBcc: evil@x.com","subject":"Hi","body":"Body"}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			tool := NewSendEmailTool("smtp.example.com", 587, "agent@example.com", "password", "", false)
			tool.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
				t.Fatal("send should not be called on header injection")
				return nil
			}
			res, err := tool.Execute(context.Background(), []byte(input))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if res.Error == "" {
				t.Fatal("Error is empty, want header-injection rejection")
			}
		})
	}
}

func TestSendEmailToolExecuteSendError(t *testing.T) {
	tool := NewSendEmailTool("smtp.example.com", 587, "agent@example.com", "password", "", false)
	tool.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		return errors.New("boom")
	}

	res, err := tool.Execute(context.Background(), []byte(`{"to":"user@example.com","subject":"Hello","body":"Body text"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.Error != "send email: boom" {
		t.Fatalf("Error = %q, want send email: boom", res.Error)
	}
}
