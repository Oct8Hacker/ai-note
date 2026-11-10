package synthesis

import (
	"ainote/internal/pipeline"
	"encoding/json"
	"errors"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"strings"
)

const outputSchema = `{
  "type": "object",
  "properties": {
    "title": {
      "type": "string"
    },
    "overview": {
      "type": "string"
    },
    "sections": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "heading": {
            "type": "string"
          },
          "start_sec": {
            "type": "number"
          },
          "end_sec": {
            "type": "number"
          },
          "bullets": {
            "type": "array",
            "items": {
              "type": "string"
            }
          }
        },
        "required": [
          "heading",
          "start_sec",
          "end_sec",
          "bullets"
        ],
        "additionalProperties": false
      }
    },
    "visuals": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "timestamp_sec": {
            "type": "number"
          },
          "section_index": {
            "type": "integer"
          },
          "caption": {
            "type": "string"
          },
          "reason": {
            "type": "string"
          },
          "priority": {
            "type": "integer"
          }
        },
        "required": [
          "timestamp_sec",
          "section_index",
          "caption",
          "reason",
          "priority"
        ],
        "additionalProperties": false
      }
    }
  },
  "required": [
    "title",
    "overview",
    "sections",
    "visuals"
  ],
  "additionalProperties": false
}`

var contract = jsonschema.MustCompileString("synthesis.json", outputSchema)

// Schema returns the structured-output contract for synthesis calls.
func Schema() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal([]byte(outputSchema), &schema); err != nil {
		panic(err)
	}
	return schema
}
func decode(text string) (pipeline.Synthesis, error) {
	var s pipeline.Synthesis
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return s, err
	}
	if err := contract.Validate(value); err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(text), &s); err != nil {
		return s, err
	}
	if strings.TrimSpace(s.Title) == "" || len(s.Sections) == 0 {
		return s, errors.New("a nonempty title and sections are required")
	}
	return s, nil
}
