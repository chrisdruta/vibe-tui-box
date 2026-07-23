package schema

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// decode strictly decodes an already-inspected source into the manifest
// types. Unknown fields are errors.
func decode(source []byte) (Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(source))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, decodeError(err)
	}
	return m, nil
}

// decodeError normalizes yaml.v3 decode failures into ErrInvalid with
// readable messages. A yaml.TypeError carries one message per field
// problem ("line 7: field foo not found in type schema.Manifest").
func decodeError(err error) error {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		msgs := make([]string, len(typeErr.Errors))
		for i, m := range typeErr.Errors {
			msgs[i] = cleanFieldNotFound(m)
		}
		return fmt.Errorf("%w: %s", domain.ErrInvalid, strings.Join(msgs, "; "))
	}
	if errors.Is(err, domain.ErrInvalid) {
		return err
	}
	return fmt.Errorf("%w: %s", domain.ErrInvalid, yamlErrMessage(err))
}

// cleanFieldNotFound rewrites yaml.v3's Go-flavored unknown-field
// message into schema terms.
func cleanFieldNotFound(msg string) string {
	msg = strings.TrimPrefix(msg, "yaml: ")
	if before, _, found := strings.Cut(msg, " not found in type "); found {
		return before + " is not a known manifest key"
	}
	return msg
}
