package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/vul-os/vulos-apps/appsplatform"
)

// Descriptor is the OPTIONAL interface a product's appsplatform.ProductAdapter
// implements to PUBLISH its surface to the MCP layer.
//
// The base ProductAdapter has Act (actions) and Read (kinds) but does not
// enumerate them — Act/Read alone cannot list themselves. A Descriptor is how a
// product tells the MCP server which actions become tools and which read-kinds
// become resources, with optional per-action JSON-Schema and descriptions.
//
// Sane default if NOT implemented: the server still works. It exposes a single
// generic passthrough tool ("act") so any action can be invoked, and an empty
// resources/list — though resources/read still works for any kind URI a caller
// already knows (the self-host escape hatch). Implement Descriptor to get a
// correct, fully described per-product server for free.
type Descriptor interface {
	// MCPTools returns the action tools this adapter exposes. Each maps to a
	// ProductAdapter.Act action.
	MCPTools() []ToolSpec

	// MCPResources returns the read-kind resources this adapter exposes. Each maps
	// to a ProductAdapter.Read kind.
	MCPResources() []ResourceSpec
}

// ToolSpec describes one MCP tool derived from an adapter Act action. Tools are
// the MUTATING surface: tools/call requires apps:write (plus any action-specific
// scope the adapter declares via RequiredScope).
type ToolSpec struct {
	// Action is the adapter action passed to ProductAdapter.Act. It is also the
	// MCP tool name unless Name overrides it.
	Action string

	// Name overrides the MCP tool name (default: Action).
	Name string

	// Description is the LLM/human-facing tool description.
	Description string

	// InputSchema is the JSON Schema for the tool arguments. The decoded arguments
	// object becomes the action Payload. nil ⇒ a permissive generic object schema.
	// If AcceptsTarget is set, a "target" string property is added automatically.
	InputSchema json.RawMessage

	// AcceptsTarget declares the tool takes a "target" argument (channel / folder
	// / room / doc id) which is lifted into ActionRequest.Target and access-checked
	// via the adapter's CanAccessTarget before Act runs.
	AcceptsTarget bool
}

// ResourceSpec describes one MCP resource derived from an adapter Read kind.
// Resources are the READ surface: resources/list and resources/read require
// apps:read (plus any kind-specific scope the adapter declares).
type ResourceSpec struct {
	// Kind is the adapter read kind passed to ProductAdapter.Read.
	Kind string

	// Name is the display name (default: Kind).
	Name string

	// Description is the human/LLM-facing description.
	Description string

	// MIMEType of the returned content (default: application/json).
	MIMEType string

	// AcceptsTarget declares the resource is addressed with a target segment
	// (vulos://<product>/<kind>/<target>) which is access-checked before Read.
	AcceptsTarget bool
}

// genericActTool is the fallback tool exposed when an adapter does not implement
// Descriptor. It passes an arbitrary action through to ProductAdapter.Act.
const genericActTool = "act"

// uriScheme is the URI scheme used for adapter-read resources:
// vulos://<product>/<kind>[/<target>][?param=value...]
const uriScheme = "vulos"

// toolEntry pairs the MCP-facing Tool with the spec used to dispatch a call.
type toolEntry struct {
	tool Tool
	spec ToolSpec
}

// resourceEntry pairs the MCP-facing Resource with the spec used to dispatch a
// read.
type resourceEntry struct {
	resource Resource
	spec     ResourceSpec
}

// deriveTools builds the MCP tool set from an adapter. When the adapter
// implements Descriptor its MCPTools drive the set; otherwise a single generic
// "act" passthrough tool is returned.
func deriveTools(adapter appsplatform.ProductAdapter) ([]toolEntry, map[string]toolEntry) {
	var specs []ToolSpec
	if d, ok := adapter.(Descriptor); ok {
		specs = d.MCPTools()
	}
	if len(specs) == 0 {
		specs = []ToolSpec{genericActSpec()}
	}
	entries := make([]toolEntry, 0, len(specs))
	byName := make(map[string]toolEntry, len(specs))
	for _, s := range specs {
		name := s.Name
		if name == "" {
			name = s.Action
		}
		if name == "" || byName[name].tool.Name != "" {
			continue // skip empty or duplicate names
		}
		e := toolEntry{
			tool: Tool{
				Name:        name,
				Description: s.Description,
				InputSchema: toolInputSchema(s),
			},
			spec: s,
		}
		entries = append(entries, e)
		byName[name] = e
	}
	return entries, byName
}

// genericActSpec is the passthrough tool used when no Descriptor is present.
func genericActSpec() ToolSpec {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "description": "Product-defined action name."},
    "target": {"type": "string", "description": "Optional target id (channel/folder/room/doc)."},
    "payload": {"type": "object", "description": "Product-specific action body."}
  },
  "required": ["action"]
}`)
	return ToolSpec{
		Action:      genericActTool,
		Name:        genericActTool,
		Description: "Perform a product action. Set `action` to the product-defined action name and `payload` to its body.",
		InputSchema: schema,
	}
}

// isGenericActSpec reports whether a spec is the generic passthrough (its
// arguments carry the action/target/payload themselves rather than being the
// payload).
func isGenericActSpec(s ToolSpec) bool { return s.Action == genericActTool && s.Name == genericActTool }

// toolInputSchema returns the JSON-Schema for a tool, defaulting to a permissive
// object and injecting a "target" property when the tool accepts one.
func toolInputSchema(s ToolSpec) json.RawMessage {
	if len(s.InputSchema) == 0 {
		if s.AcceptsTarget {
			return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target id (channel/folder/room/doc)."}}}`)
		}
		return json.RawMessage(`{"type":"object"}`)
	}
	if !s.AcceptsTarget {
		return s.InputSchema
	}
	// Inject a "target" property into the supplied object schema if absent.
	var m map[string]any
	if err := json.Unmarshal(s.InputSchema, &m); err != nil {
		return s.InputSchema
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		m["properties"] = props
	}
	if _, exists := props["target"]; !exists {
		props["target"] = map[string]any{"type": "string", "description": "Target id (channel/folder/room/doc)."}
	}
	if m["type"] == nil {
		m["type"] = "object"
	}
	out, err := json.Marshal(m)
	if err != nil {
		return s.InputSchema
	}
	return out
}

// deriveResources builds the MCP resource set from an adapter's Descriptor. With
// no Descriptor the list is empty (resources/read still works for any known kind
// URI — the escape hatch).
func deriveResources(adapter appsplatform.ProductAdapter) []resourceEntry {
	d, ok := adapter.(Descriptor)
	if !ok {
		return nil
	}
	specs := d.MCPResources()
	entries := make([]resourceEntry, 0, len(specs))
	product := adapter.Product()
	for _, s := range specs {
		if s.Kind == "" {
			continue
		}
		name := s.Name
		if name == "" {
			name = s.Kind
		}
		mime := s.MIMEType
		if mime == "" {
			mime = "application/json"
		}
		uri := resourceURI(product, s.Kind, "")
		if s.AcceptsTarget {
			// Advertise the template form so an agent knows to append a target.
			uri = resourceURI(product, s.Kind, "{target}")
		}
		entries = append(entries, resourceEntry{
			resource: Resource{
				URI:         uri,
				Name:        name,
				Description: s.Description,
				MIMEType:    mime,
			},
			spec: s,
		})
	}
	return entries
}

// resourceURI builds vulos://<product>/<kind>[/<target>].
func resourceURI(product, kind, target string) string {
	u := uriScheme + "://" + product + "/" + url.PathEscape(kind)
	if target != "" {
		u += "/" + target // target may be a {template} placeholder; leave unescaped
	}
	return u
}

// parsedResourceURI is the decoded form of a resource URI.
type parsedResourceURI struct {
	product string
	kind    string
	target  string
	params  map[string]string
}

// parseResourceURI decodes vulos://<product>/<kind>[/<target>][?k=v] into its
// parts. It tolerates a missing scheme host vs path split across url forms.
func parseResourceURI(raw string) (parsedResourceURI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return parsedResourceURI{}, fmt.Errorf("invalid resource uri: %w", err)
	}
	if u.Scheme != uriScheme {
		return parsedResourceURI{}, fmt.Errorf("unsupported uri scheme %q (want %q)", u.Scheme, uriScheme)
	}
	p := parsedResourceURI{product: u.Host, params: map[string]string{}}
	segs := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(segs) == 0 || segs[0] == "" {
		return parsedResourceURI{}, fmt.Errorf("resource uri missing kind")
	}
	kind, err := url.PathUnescape(segs[0])
	if err != nil {
		kind = segs[0]
	}
	p.kind = kind
	if len(segs) == 2 && segs[1] != "" {
		target, err := url.PathUnescape(segs[1])
		if err != nil {
			target = segs[1]
		}
		p.target = target
	}
	for k, v := range u.Query() {
		if len(v) > 0 {
			p.params[k] = v[0]
		}
	}
	return p, nil
}
