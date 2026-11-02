// Package openapi embeds the JSON form of the canonical openapi.yaml contract.
package openapi

import (
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
)

//go:generate python3 tools/export_contract.py
//go:embed openapi.json
var document []byte

func JSON() []byte { return append([]byte(nil), document...) }
func Catalog() ([]system.Operation, string, error) {
	var spec struct {
		Baseline string `json:"x-source-baseline"`
		Paths    map[string]map[string]struct {
			ID            string                 `json:"operationId"`
			Phase         string                 `json:"x-phase"`
			Security      *[]map[string][]string `json:"security"`
			Authorization struct {
				Scopes []string `json:"scopes"`
			} `json:"x-authorization"`
			Capability struct {
				Implementation string `json:"implementation"`
			} `json:"x-capability"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(document, &spec); err != nil {
		return nil, "", err
	}
	operations := []system.Operation{}
	seen := map[string]bool{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if op.ID == "" || seen[op.ID] {
				return nil, "", errors.New("invalid contract operation catalog")
			}
			seen[op.ID] = true
			operations = append(operations, system.Operation{ID: op.ID, Method: strings.ToUpper(method), Path: path, Phase: op.Phase, Scopes: append([]string{}, op.Authorization.Scopes...), Anonymous: op.Security != nil && len(*op.Security) == 0, Implemented: op.Capability.Implementation == "implemented"})
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	return operations, spec.Baseline, nil
}
