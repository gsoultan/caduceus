// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// addService fills in what the connection string leaves out from the
// service it names, service= or PGSERVICE, as libpq does: ahead of the
// environment, which fills in only what the service leaves out too. The
// service is looked for in servicefile=, PGSERVICEFILE or ~/.pg_service.conf,
// and then in pg_service.conf in PGSYSCONFDIR.
func addService(settings map[string]string) error {
	name := settings["service"]
	if name == "" {
		name = os.Getenv("PGSERVICE")
	}
	if name == "" {
		return nil
	}
	for _, path := range serviceFiles(settings["servicefile"]) {
		service, err := readService(path, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for key, value := range service {
			if _, set := settings[key]; !set {
				settings[key] = value
			}
		}
		return nil
	}
	return fmt.Errorf("%w: service %q is not defined", ErrInvalidConnString, name)
}

// serviceFiles is where to look for a service, in order.
func serviceFiles(given string) []string {
	var files []string
	switch {
	case given != "":
		files = append(files, given)
	case os.Getenv("PGSERVICEFILE") != "":
		files = append(files, os.Getenv("PGSERVICEFILE"))
	default:
		if home, err := os.UserHomeDir(); err == nil {
			files = append(files, filepath.Join(home, ".pg_service.conf"))
		}
	}
	if dir := os.Getenv("PGSYSCONFDIR"); dir != "" {
		files = append(files, filepath.Join(dir, "pg_service.conf"))
	}
	return files
}

// readService reads the settings of the service name from the file at path,
// with fs.ErrNotExist for a file that is missing or does not define it. A
// service file is INI: a [name] line begins a service, key=value lines are
// its settings, and lines starting with # are comments.
func readService(path, name string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	defer file.Close()
	var service map[string]string
	lines := bufio.NewScanner(file)
	for number := 1; lines.Scan(); number++ {
		line := strings.TrimSpace(lines.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			if service != nil {
				return service, nil
			}
			if strings.TrimSuffix(strings.TrimPrefix(line, "["), "]") == name {
				service = map[string]string{}
			}
		case service != nil:
			key, value, ok := strings.Cut(line, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "service" || key == "servicefile" {
				return nil, fmt.Errorf("%w: %s line %d: not a setting of the service", ErrInvalidConnString, filepath.Base(path), number)
			}
			service[key] = strings.TrimSpace(value)
		}
	}
	if service == nil {
		return nil, fs.ErrNotExist
	}
	return service, lines.Err()
}
