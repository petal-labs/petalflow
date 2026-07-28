package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// encodeRegistration serializes a registration to its persisted payload
// form, encrypting any sensitive config values first. scope controls secret
// key derivation and must match the scope used to decode the payload later.
func encodeRegistration(scope string, reg ToolRegistration) ([]byte, error) {
	clone := cloneRegistration(reg)
	if err := encryptSensitiveRegistration(scope, &clone); err != nil {
		return nil, err
	}
	data, err := json.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("tool: encode registration: %w", err)
	}
	return data, nil
}

// decodeRegistration deserializes a persisted payload back into a
// registration, decrypting any sensitive config values. scope must match
// the scope used when the payload was encoded.
func decodeRegistration(scope string, payload []byte) (ToolRegistration, error) {
	var reg ToolRegistration
	if err := json.Unmarshal(payload, &reg); err != nil {
		return ToolRegistration{}, fmt.Errorf("tool: decode registration: %w", err)
	}
	if err := decryptSensitiveRegistration(scope, &reg); err != nil {
		return ToolRegistration{}, err
	}
	return reg, nil
}

func encryptSensitiveRegistration(scope string, reg *ToolRegistration) error {
	if reg == nil || len(reg.Config) == 0 || len(reg.Manifest.Config) == 0 {
		return nil
	}

	codec, err := newSecretCodec(scope)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range reg.Manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := reg.Config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		encrypted, err := codec.Encrypt(value)
		if err != nil {
			return fmt.Errorf("tool: encrypt config %q for %s: %w", key, reg.Name, err)
		}
		reg.Config[key] = encrypted
	}
	return nil
}

func decryptSensitiveRegistration(scope string, reg *ToolRegistration) error {
	if reg == nil || len(reg.Config) == 0 || len(reg.Manifest.Config) == 0 {
		return nil
	}

	codec, err := newSecretCodec(scope)
	if err != nil {
		return fmt.Errorf("tool: initialize secret codec: %w", err)
	}
	for key, spec := range reg.Manifest.Config {
		if !spec.Sensitive {
			continue
		}
		value := reg.Config[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		plain, err := codec.Decrypt(value)
		if err != nil {
			return fmt.Errorf("tool: decrypt config %q for %s: %w", key, reg.Name, err)
		}
		reg.Config[key] = plain
	}
	return nil
}
