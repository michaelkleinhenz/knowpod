package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
)

const (
	// maxDocumentPages is how many pages of a document are read; longer documents are read
	// up to there.
	maxDocumentPages = 50
	// pagesPerRequest is how many notebook pages are sent to the model at once.
	pagesPerRequest = 8
	// pageImageScale sizes the page images (1 = the tablet's 1404×1872 pixels).
	pageImageScale = 0.75
	// maxDocumentPDF limits PDFs sent to the model in one piece.
	maxDocumentPDF = 20 << 20
)

const notebookPrompt = `These are pages of handwritten notes. Write down what they say in Markdown, in their original language. Do not translate, summarize or comment.
Keep the structure the writing shows: headings, lists (checkboxes as "- [ ]" and "- [x]"), tables and emphasis. Describe a drawing or diagram in one short line in italics, e.g. "_Sketch: …_". Leave out crossed-out words and page numbers, and don't add headings of your own.
Output only the Markdown. If the pages hold no writing, output nothing.`

const pdfPrompt = `Write down the text of this PDF document in Markdown, in its original language. Do not translate, summarize or comment.
Keep headings, lists and tables. Leave out page headers, footers and page numbers.
Output only the Markdown. If the document holds no text, output nothing.`

// readDocument is the stage stored → transcribed for documents: a vision model reads the
// pages (notebook pages as images, PDFs as they are) and the text becomes the transcript.
// EPUBs are not read. A non-empty language (e.g. "German") is the language the text is
// written down in.
func (s *AIService) readDocument(ctx context.Context, st *settings.OpenRouter, rec *recording.Recording, language string) error {
	if rec.Original == nil {
		return errors.New("the document's files have not been stored")
	}
	tmp, err := os.CreateTemp(s.tmpDir, rec.ID+"-*.read")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	body, err := s.objects.Get(ctx, rec.Original.Key, 0, -1)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("read stored document: %w", err)
	}
	_, err = io.Copy(tmp, body)
	body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("read stored document: %w", err)
	}
	a, err := remarkable.OpenArchive(tmp.Name())
	if err != nil {
		return err
	}
	defer a.Close()

	model := st.DocumentReader()
	start := s.clock()
	var text string
	switch a.Kind() {
	case remarkable.KindNotebook:
		text, err = s.readNotebook(ctx, st.APIKey, model, language, a)
	case remarkable.KindPDF:
		text, err = s.readPDF(ctx, st.APIKey, model, language, a, rec.Pages)
	default:
		model = "" // EPUBs aren't read
	}
	if err != nil {
		return err
	}
	rec.Transcript = &recording.Transcript{Text: text, Model: model, CreatedAt: s.clock().UTC()}
	s.log.Info("document read", "id", rec.ID, "kind", string(a.Kind()), "model", model, "chars", len(text),
		"took", s.clock().Sub(start).Round(time.Second).String())
	return nil
}

// readNotebook renders the pages with writing on them and has the model read them, a few
// pages per request.
func (s *AIService) readNotebook(ctx context.Context, apiKey, model, language string, a *remarkable.Archive) (string, error) {
	pages, err := a.Pages()
	if err != nil {
		return "", err
	}
	var written []int
	for i, p := range pages {
		if len(p) > 0 {
			written = append(written, i)
		}
	}
	skipped := 0
	if len(written) > maxDocumentPages {
		skipped = len(written) - maxDocumentPages
		written = written[:maxDocumentPages]
	}
	prompt := inLanguage(notebookPrompt, language)
	var parts []string
	for from := 0; from < len(written); from += pagesPerRequest {
		chunk := written[from:min(from+pagesPerRequest, len(written))]
		content := []any{openrouter.Text(prompt)}
		if len(written) > len(chunk) {
			content[0] = openrouter.Text(prompt + fmt.Sprintf("\nThese are pages %d to %d of a longer notebook; continue where the previous pages left off.",
				chunk[0]+1, chunk[len(chunk)-1]+1))
		}
		for _, i := range chunk {
			img, err := remarkable.RenderPNG(pages[i], pageImageScale)
			if err != nil {
				return "", fmt.Errorf("render page %d: %w", i+1, err)
			}
			content = append(content, openrouter.Image(base64.StdEncoding.EncodeToString(img), "image/png"))
		}
		answer, err := s.ai.Complete(ctx, apiKey, openrouter.Request{
			Model: model, Messages: []openrouter.Message{{Role: "user", Content: content}},
		})
		if err != nil {
			return "", fmt.Errorf("read pages %d-%d: %w", chunk[0]+1, chunk[len(chunk)-1]+1, err)
		}
		if t := strings.TrimSpace(stripFence(answer)); t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.Join(parts, "\n\n")
	if skipped > 0 && text != "" {
		text += fmt.Sprintf("\n\n_Only the first %d pages with writing were read; %d more were left out._", maxDocumentPages, skipped)
	}
	return text, nil
}

// readPDF sends the PDF to the model as it is.
func (s *AIService) readPDF(ctx context.Context, apiKey, model, language string, a *remarkable.Archive, pages int) (string, error) {
	body, size, err := a.Original()
	if err != nil {
		return "", err
	}
	defer body.Close()
	if size > maxDocumentPDF {
		s.log.Info("PDF too large to read", "bytes", size, "limit", maxDocumentPDF)
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(body, maxDocumentPDF))
	if err != nil {
		return "", err
	}
	prompt := inLanguage(pdfPrompt, language)
	if pages > maxDocumentPages {
		prompt += fmt.Sprintf("\nOnly read the first %d pages.", maxDocumentPages)
	}
	answer, err := s.ai.Complete(ctx, apiKey, openrouter.Request{
		Model: model, Messages: []openrouter.Message{{Role: "user", Content: []any{
			openrouter.Text(prompt), openrouter.PDF(base64.StdEncoding.EncodeToString(data), "document.pdf"),
		}}},
	})
	if err != nil {
		return "", fmt.Errorf("read PDF: %w", err)
	}
	text := strings.TrimSpace(stripFence(answer))
	if pages > maxDocumentPages && text != "" {
		text += fmt.Sprintf("\n\n_Only the first %d of %d pages were read._", maxDocumentPages, pages)
	}
	return text, nil
}

// stripFence removes a Markdown code fence that models sometimes wrap their answer in.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || len(t) < 6 {
		return s
	}
	t = strings.TrimSuffix(t, "```")
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		return t[i+1:]
	}
	return s
}
