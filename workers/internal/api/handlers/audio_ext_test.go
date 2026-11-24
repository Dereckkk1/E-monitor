package handlers

import "testing"

func TestExtensaoDeAudio(t *testing.T) {
	casos := []struct {
		nome string
		ext  string
		ok   bool
	}{
		{"spot.mp3", ".mp3", true},
		{"SPOT 30s.WAV", ".wav", true},
		{"a.m4a", ".m4a", true},
		{"a.aac", ".aac", true},
		{"a.mpeg", ".mpeg", true},
		// o nome que chega no multipart é do usuário: nada dele pode virar caminho
		{"../../etc/passwd", "", false},
		{"../../x.mp3/../y", "", false},
		{"musica.mp3.exe", "", false},
		{"sem-extensao", "", false},
		{".mp3", ".mp3", true}, // só extensão: ainda é um .mp3 (o nome salvo é uuid+ext)
		{"a.ogg", "", false},
		{"", "", false},
	}
	for _, c := range casos {
		ext, ok := extensaoDeAudio(c.nome)
		if ext != c.ext || ok != c.ok {
			t.Errorf("extensaoDeAudio(%q) = (%q, %v), quero (%q, %v)", c.nome, ext, ok, c.ext, c.ok)
		}
	}
}
