package plan

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	v1 "github.com/octacian/backlot/api/v1"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func resolveEnvironmentValues(m v1.Manifest, config v1.MachineConfig, c v1.Component, project string, p v1.PlanResponse) (map[string]resolvedValue, error) {
	values := map[string]resolvedValue{}
	// The launch baseline is deliberately small; no ambient credentials are copied.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SystemRoot"} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = resolvedValue{literal: &value, secret: true}
		}
	}
	layers := make([]v1.Environment, 0, len(c.EnvironmentSets)+1)
	for _, name := range c.EnvironmentSets {
		layers = append(layers, m.Environments[name])
	}
	layers = append(layers, c.Environment)
	// Apply all pass-throughs, then all seeds, then all assignments across included sets.
	for _, layer := range layers {
		for _, key := range layer.PassThrough {
			if value, ok := os.LookupEnv(key); ok {
				values[key] = resolvedValue{literal: &value, secret: true}
			}
		}
	}
	for _, layer := range layers {
		for _, relative := range layer.Seeds {
			file, err := containedFile(project, relative)
			if err != nil {
				return nil, err
			}
			data, err := readFile(file)
			if err != nil {
				return nil, err
			}
			seed, err := parseSeed(data, relative)
			if err != nil {
				return nil, err
			}
			for key, value := range seed {
				values[key] = resolvedValue{literal: &value, secret: true}
			}
		}
	}
	for _, layer := range layers {
		for key, value := range layer.Assign {
			resolved, err := resolveValue(m, config, value, project, p)
			if err != nil {
				return nil, err
			}
			values[key] = resolved
		}
	}
	for _, layer := range layers {
		for _, key := range layer.Required {
			v, ok := values[key]
			if !ok || v.literal != nil && *v.literal == "" {
				return nil, problem("missing_input", "environment."+key, "required environment value is missing or empty; supply a selected seed, pass-through or assignment")
			}
		}
	}
	return values, nil
}

func parseSeed(data []byte, file string) (map[string]string, error) {
	fail := func() (map[string]string, error) {
		return nil, problem("invalid_seed", file, "expected literal KEY=value lines; no export, multiline values or shell syntax in keys")
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return fail()
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), len(data)+1)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !keyPattern.MatchString(key) {
			return fail()
		}
		if _, exists := values[key]; exists {
			return nil, problem("invalid_seed", file, "duplicate seed key; use separate ordered files for overrides")
		}
		values[key] = value
	}
	if scanner.Err() != nil {
		return fail()
	}
	return values, nil
}
