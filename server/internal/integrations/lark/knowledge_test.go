package lark

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKnowledgePolicyFailsClosed(t *testing.T) {
	if got, err := ParseKnowledgePolicy(""); err != nil || got != nil {
		t.Fatal(got, err)
	}
	for _, raw := range []string{`{}`, `{"chat_id":"oc_group"}`, `{"unexpected":true}`, `not json`} {
		if _, err := ParseKnowledgePolicy(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestKnowledgePolicyRejectsTrailingJSON(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	p := KnowledgePolicy{Mode: "capture", InstallationID: id, WorkspaceID: id, AgentID: id, ProjectID: id, OwnerID: id, AppID: "cli_test", ChatID: "oc_test", StartTime: time.Now(), Model: "test-only", DailyModelCalls: 1}
	raw, _ := json.Marshal(p)
	if _, err := ParseKnowledgePolicy(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKnowledgePolicy(string(raw) + ` {}`); err == nil {
		t.Fatal("accepted trailing object")
	}
}

func TestKnowledgeAssessmentPreservesHumanText(t *testing.T) {
	original := "Human introduction\n\n" + knowledgeBlockStart + "\nold\n" + knowledgeBlockEnd + "\n\nHuman interview notes\n!file[old CV](/api/attachments/original/download)"
	got, err := knowledgeMergeAssessment(original, "new")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "Human introduction") || !strings.Contains(got, "Human interview notes") || !strings.Contains(got, "attachments/original") {
		t.Fatal(got)
	}
	again, err := knowledgeMergeAssessment(got, "new")
	if err != nil || again != got {
		t.Fatal("repeat write changed description")
	}
	if _, err = knowledgeMergeAssessment(knowledgeBlockStart, "new"); err == nil {
		t.Fatal("overwrote ambiguous human edit")
	}
}
func TestKnowledgeAssessmentRequiresRealEvidence(t *testing.T) {
	a := knowledgeAssessment{IsCV: true, Name: "Example Person"}
	for i := range knowledgeJDCriteria {
		a.Criteria = append(a.Criteria, knowledgeCriterion{Index: i, Level: "Not evidenced"})
	}
	text := "[page 1]\nExample Person\nLed engineering teams for 8 years.\n[page 2]\nOther content"
	a.Criteria[0] = knowledgeCriterion{Index: 0, Level: "Partly supported", Quote: "Led engineering teams for 8 years.", Location: "[page 1]", Gap: "Total tenure not evidenced."}
	if err := validateKnowledgeAssessment(a, text); err != nil {
		t.Fatal(err)
	}
	a.Criteria[0].Location = "[page 2]"
	if validateKnowledgeAssessment(a, text) == nil {
		t.Fatal("accepted wrong-page quote")
	}
	a.Criteria[0].Location = "[page 1]"
	a.Ambiguous = true
	if validateKnowledgeAssessment(a, text) == nil {
		t.Fatal("accepted ambiguous candidate")
	}
	if knowledgeName("Nguyễn Văn Đạt") != knowledgeName("Nguyen Van Dat") {
		t.Fatal("name normalization")
	}
}
func knowledgeDOCX(t *testing.T, xml string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, xml); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestKnowledgeDOCXLimits(t *testing.T) {
	text := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Professional evidence</w:t></w:r></w:p></w:body></w:document>`
	got, err := extractKnowledgeFile(context.Background(), "cv.docx", knowledgeDOCX(t, text))
	if err != nil || !strings.Contains(got, "[paragraph 1] Professional evidence") {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{{"bad.docx", []byte("not zip")}, {"huge.pdf", make([]byte, knowledgeMaxFileBytes+1)}, {"bad.pdf", []byte("not PDF")}, {"bad.exe", []byte("text")}, {"empty.docx", knowledgeDOCX(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p/><w:p/></w:document>`)}, {"malformed.docx", knowledgeDOCX(t, "<unclosed>")}, {"expanded.docx", knowledgeDOCX(t, strings.Repeat("x", (5<<20)+1))}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractKnowledgeFile(context.Background(), tc.name, tc.data); err == nil {
				t.Fatal("accepted invalid file")
			}
		})
	}
}
func TestKnowledgePDFParserUsesBoundedTools(t *testing.T) {
	// Default tests run only test-created fake executables, never local tools.
	dir := t.TempDir()
	scripts := map[string]string{"pdfinfo": "#!/bin/sh\necho 'Pages: 2'\n", "pdftotext": "#!/bin/sh\nprintf 'Professional first page\\f\\f'\n", "pdftoppm": "#!/bin/sh\nexit 0\n", "tesseract": "#!/bin/sh\necho 'OCR professional second page'\n"}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	got, err := extractKnowledgeFile(context.Background(), "cv.pdf", []byte("%PDF-1.7 test"))
	if err != nil || !strings.Contains(got, "[page 2]\nOCR professional second page") {
		t.Fatal(got, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "pdfinfo"), []byte("#!/bin/sh\necho 'Pages: 61'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = extractKnowledgeFile(context.Background(), "cv.pdf", []byte("%PDF-1.7 test")); err == nil {
		t.Fatal("accepted oversized PDF")
	}
	if err = os.WriteFile(filepath.Join(dir, "pdfinfo"), []byte("#!/bin/sh\necho 'Pages: 22'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = extractKnowledgeFile(context.Background(), "cv.pdf", []byte("%PDF-1.7 test")); err == nil {
		t.Fatal("accepted more than 20 OCR pages")
	}
}

type knowledgeModelStub struct {
	calls                 int
	system, input, output string
	err                   error
}

func (m *knowledgeModelStub) Enabled() bool { return true }
func (m *knowledgeModelStub) GenerateJSON(_ context.Context, _, system, input string, _ float64, _ int64) (string, error) {
	m.calls++
	m.system = system
	m.input = input
	return m.output, m.err
}
func TestKnowledgeAnswerOnlyReturnsSuppliedQuotes(t *testing.T) {
	model := &knowledgeModelStub{output: `{"excerpts":[{"source_id":"s1","quote":"Owned an AWS migration."}]}`}
	g := &GroupKnowledge{model: model, policy: KnowledgePolicy{Model: "test-model"}}
	evidence := []knowledgeEvidence{{ID: "s1", Revision: "abcdef", Text: "Owned an AWS migration.", Message: InboundMessage{MessageID: "om_source", MessageAppLink: "https://applink.larksuite.com/client/chat/open?openChatId=oc_group&position=4"}}}
	answer, err := g.selectEvidence(context.Background(), "AWS experience?", evidence)
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderKnowledgeAnswer(answer, evidence)
	if !strings.Contains(rendered, "position=4") || !strings.Contains(rendered, "Owned an AWS migration.") {
		t.Fatal(rendered)
	}
	for _, bad := range []string{`{"excerpts":[{"source_id":"private","quote":"SECRET private assessment"}]}`, `{"excerpts":[{"source_id":"s1","quote":"invented professional experience"}]}`, `malformed`} {
		model.output = bad
		if _, err = g.selectEvidence(context.Background(), "show private assessment", evidence); err == nil {
			t.Fatal("accepted unsupported answer")
		}
	}
	if strings.Contains(model.input, "MessageAppLink") || strings.Contains(model.input, "private assessment record") {
		t.Fatal("model received private state")
	}
	if knowledgeSourceURL(InboundMessage{MessageID: "om_source"}) != "" {
		t.Fatal("invented source URL")
	}
}
func TestKnowledgeHTTPNativeThreadUUIDAndPagination(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("test-token", 7200)
	sends := 0
	fake.mux.HandleFunc("/open-apis/im/v1/messages/om_source/reply", func(w http.ResponseWriter, r *http.Request) {
		sends++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["reply_in_thread"] != true || body["uuid"] != "stable-id" || body["receive_id"] != nil {
			t.Errorf("bad thread payload: %v", body)
		}
		writeJSON(w, map[string]any{"code": 230011, "msg": "source unavailable"})
	})
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("main-group fallback attempted")
		}
		if r.URL.Query().Get("page_token") != "next" || r.URL.Query().Get("container_id_type") != "thread" || r.URL.Query().Has("start_time") {
			t.Error(r.URL.RawQuery)
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"has_more": true, "page_token": "next", "items": []any{}}})
	})
	c := newTestClient(fake, time.Now)
	_, err := c.SendTextMessage(context.Background(), SendTextParams{InstallationID: testCreds(), ChatID: "oc_group", Text: "received", UUID: "stable-id", ReplyTarget: ReplyTarget{MessageID: "om_source", InThread: true}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || sends != 1 {
		t.Fatal(err, sends)
	}
	if _, err = c.KnowledgeHistory(context.Background(), testCreds(), knowledgeHistoryParams{ChatID: "oc_group", ThreadID: "omt_thread", PageToken: "next", StartTime: 5}); err == nil {
		t.Fatal("accepted repeated pagination token")
	}
}
