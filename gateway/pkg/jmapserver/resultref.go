package jmapserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// resolvedResponse is an earlier response of the same request, decoded on first use.
type resolvedResponse struct {
	inv     invocation
	doc     any
	decoded bool
}

type resultReference struct {
	ResultOf string `json:"resultOf"`
	Name     string `json:"name"`
	Path     string `json:"path"`
}

// resolveReferences replaces every "#arg" with the value its ResultReference
// points at (RFC 8620 §3.7) and returns the arguments as a JSON object.
func resolveReferences(args map[string]json.RawMessage, done []resolvedResponse) (json.RawMessage, *MethodError) {
	out := make(map[string]json.RawMessage, len(args))
	for k, v := range args {
		if !isReferenceKey(k) {
			out[k] = v
			continue
		}
		name := k[1:]
		if _, dup := args[name]; dup {
			return nil, InvalidArguments("both " + name + " and " + k + " are given")
		}
		var ref resultReference
		if err := json.Unmarshal(v, &ref); err != nil || ref.ResultOf == "" || ref.Name == "" {
			return nil, invalidResultReference(k + " is not a ResultReference")
		}
		val, err := evaluate(ref, done)
		if err != nil {
			return nil, invalidResultReference(k + ": " + err.Error())
		}
		raw, err := json.Marshal(val)
		if err != nil {
			return nil, invalidResultReference(k + ": " + err.Error())
		}
		out[k[1:]] = raw
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, InvalidArguments(err.Error())
	}
	return raw, nil
}

func evaluate(ref resultReference, done []resolvedResponse) (any, error) {
	for i := range done {
		r := &done[i]
		if r.inv.callID != ref.ResultOf {
			continue
		}
		// The first response with this call id decides; an error response or an
		// implicit response with another name makes the reference invalid.
		if r.inv.name != ref.Name {
			return nil, errors.New("response " + ref.ResultOf + " is " + r.inv.name + ", not " + ref.Name)
		}
		if !r.decoded {
			raw, err := json.Marshal(r.inv.args)
			if err != nil {
				return nil, err
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&r.doc); err != nil {
				return nil, err
			}
			r.decoded = true
		}
		return evalPointer(r.doc, ref.Path)
	}
	return nil, errors.New("no response with call id " + ref.ResultOf)
}

// evalPointer applies a JSON Pointer (RFC 6901) with JMAP's "*" extension: on an
// array, "*" applies the rest of the pointer to every item and concatenates the
// results, flattening results that are arrays themselves.
func evalPointer(doc any, path string) (any, error) {
	if path == "" {
		return doc, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("path must start with /")
	}
	tokens := strings.Split(path[1:], "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return walk(doc, tokens)
}

func walk(v any, tokens []string) (any, error) {
	if len(tokens) == 0 {
		return v, nil
	}
	t, rest := tokens[0], tokens[1:]
	switch x := v.(type) {
	case map[string]any:
		child, ok := x[t]
		if !ok {
			return nil, errors.New("no member " + strconv.Quote(t))
		}
		return walk(child, rest)
	case []any:
		if t == "*" {
			out := []any{}
			for _, item := range x {
				r, err := walk(item, rest)
				if err != nil {
					return nil, err
				}
				if arr, ok := r.([]any); ok {
					out = append(out, arr...)
				} else {
					out = append(out, r)
				}
			}
			return out, nil
		}
		i, err := arrayIndex(t, len(x))
		if err != nil {
			return nil, err
		}
		return walk(x[i], rest)
	default:
		return nil, errors.New("cannot apply " + strconv.Quote(t) + " to a scalar")
	}
}

func arrayIndex(t string, n int) (int, error) {
	if t == "" || (len(t) > 1 && t[0] == '0') {
		return 0, errors.New("bad array index " + strconv.Quote(t))
	}
	i, err := strconv.Atoi(t)
	if err != nil || i < 0 || i >= n {
		return 0, errors.New("array index " + strconv.Quote(t) + " out of range")
	}
	return i, nil
}
