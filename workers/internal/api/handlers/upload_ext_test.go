package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// multipartUpload monta o POST de upload com os campos obrigatórios e um
// arquivo `audio` com o nome pedido (o nome é o que vem do usuário).
func multipartUpload(t *testing.T, campos map[string]string, nomeArquivo string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range campos {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("audio", nomeArquivo)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("não é áudio"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// Extensão fora da lista (ou nome montado pra escapar do diretório) é recusada
// ANTES de qualquer arquivo ser criado em MastersPath.
func TestUpload_RecusaExtensaoForaDaLista(t *testing.T) {
	nomes := []string{"spot.ogg", "../../etc/passwd", "x.mp3/../y", "musica.mp3.exe", "sem-extensao"}

	for _, nome := range nomes {
		t.Run("materials/"+nome, func(t *testing.T) {
			dir := t.TempDir()
			h := &MaterialsHandler{MastersPath: dir}
			rec := httptest.NewRecorder()
			h.Upload(rec, multipartUpload(t, map[string]string{
				"client_id": uuid.NewString(), "title": "Spot",
			}, nome))
			conferirRecusa(t, rec, dir)
		})
		t.Run("commercials/"+nome, func(t *testing.T) {
			dir := t.TempDir()
			h := &CommercialsHandler{MastersPath: dir}
			rec := httptest.NewRecorder()
			h.Upload(rec, multipartUpload(t, map[string]string{
				"campaign_id": uuid.NewString(), "title": "Spot",
			}, nome))
			conferirRecusa(t, rec, dir)
		})
	}
}

func conferirRecusa(t *testing.T, rec *httptest.ResponseRecorder, dir string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo: %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported audio format") {
		t.Fatalf("corpo = %q, quero a recusa de formato", rec.Body.String())
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != 0 {
		t.Fatalf("nada podia ter sido gravado em MastersPath, achei %d arquivo(s)", len(entradas))
	}
}
