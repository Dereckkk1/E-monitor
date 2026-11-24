package handlers

import (
	"path/filepath"
	"strings"
)

// extensaoDeAudio decide a extensão do master enviado. O valor devolvido é
// SEMPRE um literal desta função — nunca um pedaço do nome que veio no
// multipart. Por isso o caminho montado com ele (MastersPath + uuid + ext) e o
// ffprobe que recebe esse caminho não carregam nada controlado pelo usuário.
//
// Antes a extensão era `strings.ToLower(filepath.Ext(header.Filename))` checada
// contra a lista: igualmente seguro, mas o valor usado ainda era o do usuário, e
// o SonarCloud acusava path traversal (gosecurity:S2083) e injeção de argumento
// no ffprobe (gosecurity:S6350). Mesmo comportamento, sem a mancha.
func extensaoDeAudio(nomeEnviado string) (string, bool) {
	switch strings.ToLower(filepath.Ext(nomeEnviado)) {
	case ".wav":
		return ".wav", true
	case ".mp3":
		return ".mp3", true
	case ".m4a":
		return ".m4a", true
	case ".aac":
		return ".aac", true
	case ".mpeg":
		return ".mpeg", true
	}
	return "", false
}
