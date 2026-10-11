package quality

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Environment reads data, never sources a shell script or expands substitutions.
// File values override ambient values; nothing is written to the process env.
func Environment(path string, ambient func(string) string) (func(string) string, error) {
	values := map[string]string{}
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > 256<<10 {
			return nil, errors.New("env file exceeds 256 KiB")
		}
		s := bufio.NewScanner(bytes.NewReader(b))
		line := 0
		for s.Scan() {
			line++
			text := strings.TrimSpace(s.Text())
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			text = strings.TrimPrefix(text, "export ")
			key, value, ok := strings.Cut(text, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !ok || !envKey.MatchString(key) || strings.ContainsRune(value, 0) {
				return nil, fmt.Errorf("invalid env assignment at line %d", line)
			}
			if _, exists := values[key]; exists {
				return nil, fmt.Errorf("duplicate env assignment at line %d", line)
			}
			if strings.HasPrefix(value, "\"") {
				value, err = strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("invalid quoted env value at line %d", line)
				}
			} else if strings.HasPrefix(value, "'") {
				if len(value) < 2 || !strings.HasSuffix(value, "'") {
					return nil, fmt.Errorf("invalid quoted env value at line %d", line)
				}
				value = value[1 : len(value)-1]
			} else if at := strings.Index(value, " #"); at >= 0 {
				value = strings.TrimSpace(value[:at])
			}
			values[key] = value
			if len(values) > 512 {
				return nil, errors.New("too many env assignments")
			}
		}
		if err := s.Err(); err != nil {
			return nil, errors.New("env line exceeds size limit")
		}
	}
	return func(k string) string {
		// Platform delivery is outside the local quality test, even if a server
		// config containing bot credentials was supplied.
		if strings.HasPrefix(k, "TELEGRAM_") || strings.HasPrefix(k, "QQ_") || strings.HasPrefix(k, "ONEBOT_") {
			return ""
		}
		if v, ok := values[k]; ok {
			return v
		}
		return ambient(k)
	}, nil
}
