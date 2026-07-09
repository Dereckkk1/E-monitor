package handlers

import "testing"

// TestResolveManualAudioExt cobre a resolução de formato do áudio de censura:
// primeiro pelo Content-Type do browser, e — quando esse não bate no
// manualAudioMIME — pela extensão do filename (espelhando a validação do upload
// de material, que é por extensão). O caso que motivou o fallback: .mpeg chega
// como video/mpeg (ou octet-stream/vazio) e antes tomava 415.
func TestResolveManualAudioExt(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		filename    string
		wantExt     string
		wantStoreCT string
		wantOK      bool
	}{
		// Content-Type reconhecido → mantém o content-type do browser.
		{"mp3 by mime", "audio/mpeg", "spot.mp3", "mp3", "audio/mpeg", true},
		{"m4a by mime", "audio/mp4", "spot.m4a", "m4a", "audio/mp4", true},
		{"wav by mime x-wav", "audio/x-wav", "spot.wav", "wav", "audio/x-wav", true},
		{"mime with params", "audio/mpeg; charset=binary", "spot.mp3", "mp3", "audio/mpeg; charset=binary", true},
		{"mime uppercase", "AUDIO/MPEG", "spot.mp3", "mp3", "AUDIO/MPEG", true},

		// Content-Type NÃO reconhecido → fallback por extensão, com content-type canônico.
		{"mpeg as video mime falls back to ext", "video/mpeg", "censura.mpeg", "mp3", "audio/mpeg", true},
		{"empty mime falls back to ext", "", "censura.m4a", "m4a", "audio/mp4", true},
		{"octet-stream falls back to ext", "application/octet-stream", "censura.wav", "wav", "audio/wav", true},
		{"aac by ext", "application/octet-stream", "censura.AAC", "aac", "audio/aac", true},
		{"ogg by ext", "", "censura.ogg", "ogg", "audio/ogg", true},

		// Nem MIME nem extensão de áudio conhecida → rejeita (415).
		{"unknown ext and mime", "application/pdf", "doc.pdf", "", "", false},
		{"no extension unknown mime", "application/octet-stream", "noext", "", "", false},
		{"empty everything", "", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext, storeCT, ok := resolveManualAudioExt(c.contentType, c.filename)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !c.wantOK {
				return
			}
			if ext != c.wantExt {
				t.Errorf("ext = %q, want %q", ext, c.wantExt)
			}
			if storeCT != c.wantStoreCT {
				t.Errorf("storeContentType = %q, want %q", storeCT, c.wantStoreCT)
			}
		})
	}
}
