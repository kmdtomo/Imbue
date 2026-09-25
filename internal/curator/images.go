package curator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"imbue/internal/evidence"
	"imbue/internal/local"
)

var imageDataURL = regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=_\r\n-]+`)

// Images are retained in the immutable evidence store, but never sent as base64
// text or interpreted as visual evidence by the text-only curator.
func excludeImages(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	changed := false
	marker := func(v any) any {
		b, _ := json.Marshal(v)
		changed = true
		return map[string]any{"image_omitted": true, "reason": "画像は学習入力対象外。内容は未確認。", "sha256": local.Hash(b), "original_bytes": len(b)}
	}
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if omitted, _ := x["image_omitted"].(bool); omitted {
				return v
			}
			typ, _ := x["type"].(string)
			mime, _ := x["mimeType"].(string)
			if mime == "" {
				mime, _ = x["mime_type"].(string)
			}
			if typ == "image" || typ == "input_image" || typ == "image_url" || strings.HasPrefix(mime, "image/") {
				return marker(x)
			}
			for k, item := range x {
				// Provider-specific screenshot URLs and image paths are not visual input.
				switch strings.ToLower(k) {
				case "image_url", "imageurl", "image_path", "imagepath", "screenshot":
					if m, ok := item.(map[string]any); ok && m["image_omitted"] == true {
						continue
					}
					if item != nil {
						x[k] = marker(item)
					}
				default:
					x[k] = visit(item)
				}
			}
		case []any:
			for i, item := range x {
				x[i] = visit(item)
			}
		case string:
			if imageDataURL.MatchString(x) {
				changed = true
				return imageDataURL.ReplaceAllStringFunc(x, func(s string) string {
					return fmt.Sprintf("[画像除外: sha256=%s bytes=%d 内容未確認]", local.Hash([]byte(s)), len(s))
				})
			}
		}
		return v
	}
	value = visit(value)
	if !changed {
		return raw, nil
	}
	return json.Marshal(value)
}

func excludePacketImages(p Packet) (Packet, error) {
	clean := func(events []evidence.Event) ([]evidence.Event, error) {
		out := append([]evidence.Event(nil), events...)
		for i := range out {
			b, err := excludeImages(out[i].Payload)
			if err != nil {
				return nil, fmt.Errorf("image exclusion for %s: %w", out[i].ID, err)
			}
			out[i].Payload = b
		}
		return out, nil
	}
	var err error
	if p.Events, err = clean(p.Events); err != nil {
		return p, err
	}
	if p.ContextEvents, err = clean(p.ContextEvents); err != nil {
		return p, err
	}
	p.ExistingCases = append([]ExistingCase(nil), p.ExistingCases...)
	for i := range p.ExistingCases {
		p.ExistingCases[i].Body, err = excludeImages(p.ExistingCases[i].Body)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}
