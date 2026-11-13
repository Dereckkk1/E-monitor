package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// estado é o registro local do que já foi importado: id daqui → id do hub. Fica
// FORA do git (padrão: /tmp do container). Serve para não subir bytes de novo;
// a garantia de não duplicar é do hub (`ja-importada`), não deste arquivo.
type estado struct {
	caminho    string
	Importadas map[string]string `json:"importadas"`
}

func carregaEstado(caminho string) (*estado, error) {
	e := &estado{caminho: caminho, Importadas: map[string]string{}}
	b, err := os.ReadFile(caminho)
	if errors.Is(err, fs.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, e); err != nil {
		return nil, err
	}
	if e.Importadas == nil {
		e.Importadas = map[string]string{}
	}
	return e, nil
}

func (e *estado) ja(id string) bool { _, ok := e.Importadas[id]; return ok }

// marca grava a cada sugestão, por arquivo temporário + rename: um Ctrl+C no
// meio não deixa um JSON pela metade (que o próximo carregaEstado recusaria).
func (e *estado) marca(id, sugestaoID string) error {
	e.Importadas[id] = sugestaoID
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.caminho + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.caminho)
}
