package petalflow

import (
	"context"
	"errors"
	"testing"
)

func TestNewTransformNode(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		node := NewTransformNode("transform1", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "input",
			OutputVar: "output",
			Fields:    []string{"name"},
		})

		if node.ID() != "transform1" {
			t.Errorf("expected ID 'transform1', got %q", node.ID())
		}
		if node.Kind() != NodeKindTransform {
			t.Errorf("expected kind %v, got %v", NodeKindTransform, node.Kind())
		}

		config := node.Config()
		if config.Format != "json" {
			t.Errorf("expected default format 'json', got %q", config.Format)
		}
		if config.Separator != "." {
			t.Errorf("expected default separator '.', got %q", config.Separator)
		}
		if config.MergeStrategy != "shallow" {
			t.Errorf("expected default merge strategy 'shallow', got %q", config.MergeStrategy)
		}
	})
}

func TestTransformNode_Pick(t *testing.T) {
	t.Run("picks specified fields", func(t *testing.T) {
		node := NewTransformNode("pick", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "user",
			OutputVar: "result",
			Fields:    []string{"name", "email"},
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{
			"name":     "John",
			"email":    "john@example.com",
			"password": "secret",
			"age":      30,
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["name"] != "John" {
			t.Errorf("expected name 'John', got %v", output["name"])
		}
		if output["email"] != "john@example.com" {
			t.Errorf("expected email 'john@example.com', got %v", output["email"])
		}
		if _, exists := output["password"]; exists {
			t.Error("password should not be picked")
		}
		if _, exists := output["age"]; exists {
			t.Error("age should not be picked")
		}
	})

	t.Run("picks nested fields", func(t *testing.T) {
		node := NewTransformNode("pickNested", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "data",
			OutputVar: "result",
			Fields:    []string{"user.name", "meta.score"},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"user": map[string]any{
				"name":  "John",
				"email": "john@example.com",
			},
			"meta": map[string]any{
				"score":  0.95,
				"source": "api",
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		user := output["user"].(map[string]any)
		if user["name"] != "John" {
			t.Errorf("expected user.name 'John', got %v", user["name"])
		}

		meta := output["meta"].(map[string]any)
		if meta["score"] != 0.95 {
			t.Errorf("expected meta.score 0.95, got %v", meta["score"])
		}
	})

	t.Run("error without fields", func(t *testing.T) {
		node := NewTransformNode("pickNoFields", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "data",
			OutputVar: "result",
			// No Fields
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "test"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for pick without Fields")
		}
	})
}

func TestTransformNode_Omit(t *testing.T) {
	t.Run("omits specified fields", func(t *testing.T) {
		node := NewTransformNode("omit", TransformNodeConfig{
			Transform: TransformOmit,
			InputVar:  "user",
			OutputVar: "result",
			Fields:    []string{"password", "internal_id"},
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{
			"name":        "John",
			"email":       "john@example.com",
			"password":    "secret",
			"internal_id": 12345,
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["name"] != "John" {
			t.Errorf("expected name 'John', got %v", output["name"])
		}
		if output["email"] != "john@example.com" {
			t.Errorf("expected email preserved, got %v", output["email"])
		}
		if _, exists := output["password"]; exists {
			t.Error("password should be omitted")
		}
		if _, exists := output["internal_id"]; exists {
			t.Error("internal_id should be omitted")
		}
	})

	t.Run("omits nested fields", func(t *testing.T) {
		node := NewTransformNode("omitNested", TransformNodeConfig{
			Transform: TransformOmit,
			InputVar:  "data",
			OutputVar: "result",
			Fields:    []string{"user.password", "meta.internal"},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"user": map[string]any{
				"name":     "John",
				"password": "secret",
			},
			"meta": map[string]any{
				"score":    0.95,
				"internal": true,
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		user := output["user"].(map[string]any)
		if user["name"] != "John" {
			t.Errorf("expected user.name preserved")
		}
		if _, exists := user["password"]; exists {
			t.Error("user.password should be omitted")
		}
	})
}

func TestTransformNode_Rename(t *testing.T) {
	t.Run("renames fields", func(t *testing.T) {
		node := NewTransformNode("rename", TransformNodeConfig{
			Transform: TransformRename,
			InputVar:  "data",
			OutputVar: "result",
			Mapping: map[string]string{
				"old_name": "new_name",
				"score":    "confidence",
				"user_id":  "userId",
			},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"old_name": "value1",
			"score":    0.95,
			"user_id":  123,
			"other":    "unchanged",
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["new_name"] != "value1" {
			t.Errorf("expected new_name='value1', got %v", output["new_name"])
		}
		if output["confidence"] != 0.95 {
			t.Errorf("expected confidence=0.95, got %v", output["confidence"])
		}
		if output["userId"] != 123 {
			t.Errorf("expected userId=123, got %v", output["userId"])
		}
		if output["other"] != "unchanged" {
			t.Errorf("expected other='unchanged', got %v", output["other"])
		}
		if _, exists := output["old_name"]; exists {
			t.Error("old_name should be renamed")
		}
	})

	t.Run("renames nested fields", func(t *testing.T) {
		node := NewTransformNode("renameNested", TransformNodeConfig{
			Transform: TransformRename,
			InputVar:  "data",
			OutputVar: "result",
			Mapping: map[string]string{
				"meta.old_score": "meta.new_score",
			},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"meta": map[string]any{
				"old_score": 0.95,
				"other":     "preserved",
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		meta := output["meta"].(map[string]any)
		if meta["new_score"] != 0.95 {
			t.Errorf("expected meta.new_score=0.95, got %v", meta["new_score"])
		}
		if _, exists := meta["old_score"]; exists {
			t.Error("meta.old_score should be renamed")
		}
	})
}

func TestTransformNode_Flatten(t *testing.T) {
	t.Run("flattens nested map", func(t *testing.T) {
		node := NewTransformNode("flatten", TransformNodeConfig{
			Transform: TransformFlatten,
			InputVar:  "data",
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"user": map[string]any{
				"name": "John",
				"address": map[string]any{
					"city":    "NYC",
					"country": "USA",
				},
			},
			"score": 0.95,
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["user.name"] != "John" {
			t.Errorf("expected user.name='John', got %v", output["user.name"])
		}
		if output["user.address.city"] != "NYC" {
			t.Errorf("expected user.address.city='NYC', got %v", output["user.address.city"])
		}
		if output["score"] != 0.95 {
			t.Errorf("expected score=0.95, got %v", output["score"])
		}
	})

	t.Run("flattens with custom separator", func(t *testing.T) {
		node := NewTransformNode("flattenCustom", TransformNodeConfig{
			Transform: TransformFlatten,
			InputVar:  "data",
			OutputVar: "result",
			Separator: "_",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"user": map[string]any{
				"name": "John",
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["user_name"] != "John" {
			t.Errorf("expected user_name='John', got %v", output["user_name"])
		}
	})

	t.Run("respects max depth", func(t *testing.T) {
		node := NewTransformNode("flattenDepth", TransformNodeConfig{
			Transform: TransformFlatten,
			InputVar:  "data",
			OutputVar: "result",
			MaxDepth:  1,
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{
			"level1": map[string]any{
				"level2": map[string]any{
					"level3": "deep",
				},
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		// At max depth 1, level2 should remain as a map
		level2, ok := output["level1.level2"].(map[string]any)
		if !ok {
			t.Errorf("expected level1.level2 to be a map at depth 1")
		} else if level2["level3"] != "deep" {
			t.Errorf("expected level3='deep' in nested map")
		}
	})
}

func TestTransformNode_Merge(t *testing.T) {
	t.Run("shallow merge", func(t *testing.T) {
		node := NewTransformNode("merge", TransformNodeConfig{
			Transform:     TransformMerge,
			InputVars:     []string{"a", "b", "c"},
			OutputVar:     "result",
			MergeStrategy: "shallow",
		})

		env := NewEnvelope()
		env.SetVar("a", map[string]any{"name": "John", "age": 30})
		env.SetVar("b", map[string]any{"email": "john@example.com"})
		env.SetVar("c", map[string]any{"age": 31}) // Overwrites a.age

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["name"] != "John" {
			t.Errorf("expected name='John', got %v", output["name"])
		}
		if output["email"] != "john@example.com" {
			t.Errorf("expected email from b")
		}
		if output["age"] != 31 {
			t.Errorf("expected age=31 (from c), got %v", output["age"])
		}
	})

	t.Run("deep merge", func(t *testing.T) {
		node := NewTransformNode("deepMerge", TransformNodeConfig{
			Transform:     TransformMerge,
			InputVars:     []string{"a", "b"},
			OutputVar:     "result",
			MergeStrategy: "deep",
		})

		env := NewEnvelope()
		env.SetVar("a", map[string]any{
			"user": map[string]any{
				"name": "John",
				"age":  30,
			},
		})
		env.SetVar("b", map[string]any{
			"user": map[string]any{
				"email": "john@example.com",
			},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		user := output["user"].(map[string]any)
		if user["name"] != "John" {
			t.Errorf("expected user.name='John'")
		}
		if user["age"] != 30 {
			t.Errorf("expected user.age=30")
		}
		if user["email"] != "john@example.com" {
			t.Errorf("expected user.email from deep merge")
		}
	})

	t.Run("handles non-map values", func(t *testing.T) {
		node := NewTransformNode("mergeNonMap", TransformNodeConfig{
			Transform: TransformMerge,
			InputVars: []string{"name", "score"},
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("name", "John")
		env.SetVar("score", 0.95)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["name"] != "John" {
			t.Errorf("expected name='John'")
		}
		if output["score"] != 0.95 {
			t.Errorf("expected score=0.95")
		}
	})
}

func TestTransformNode_Template(t *testing.T) {
	t.Run("renders basic template", func(t *testing.T) {
		node := NewTransformNode("template", TransformNodeConfig{
			Transform: TransformTemplate,
			Template:  "Hello, {{.name}}! Your score is {{.score}}.",
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("name", "John")
		env.SetVar("score", 0.95)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		expected := "Hello, John! Your score is 0.95."
		if output != expected {
			t.Errorf("expected %q, got %q", expected, output)
		}
	})

	t.Run("uses template functions", func(t *testing.T) {
		node := NewTransformNode("templateFuncs", TransformNodeConfig{
			Transform: TransformTemplate,
			Template:  "Name: {{upper .name}}, Tags: {{join .tags \", \"}}",
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("name", "john")
		env.SetVar("tags", []string{"go", "rust", "python"})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		expected := "Name: JOHN, Tags: go, rust, python"
		if output != expected {
			t.Errorf("expected %q, got %q", expected, output)
		}
	})

	t.Run("json function", func(t *testing.T) {
		node := NewTransformNode("templateJson", TransformNodeConfig{
			Transform: TransformTemplate,
			Template:  `{"user": {{json .user}}}`,
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("user", map[string]any{"name": "John", "age": 30})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		if output != `{"user": {"age":30,"name":"John"}}` {
			t.Errorf("unexpected json output: %s", output)
		}
	})

	t.Run("default function", func(t *testing.T) {
		node := NewTransformNode("templateDefault", TransformNodeConfig{
			Transform: TransformTemplate,
			Template:  `Name: {{default "Unknown" .name}}`,
			OutputVar: "result",
		})

		env := NewEnvelope()
		// name is not set

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		if output != "Name: Unknown" {
			t.Errorf("expected 'Name: Unknown', got %q", output)
		}
	})

	t.Run("error for invalid template", func(t *testing.T) {
		node := NewTransformNode("badTemplate", TransformNodeConfig{
			Transform: TransformTemplate,
			Template:  "{{.name",
			OutputVar: "result",
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for invalid template")
		}
	})
}

func TestTransformNode_Stringify(t *testing.T) {
	t.Run("stringifies to json", func(t *testing.T) {
		node := NewTransformNode("stringify", TransformNodeConfig{
			Transform: TransformStringify,
			InputVar:  "data",
			OutputVar: "result",
			Format:    "json",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John", "age": 30})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		if output == "" {
			t.Error("expected non-empty json string")
		}
		// Should be pretty-printed
		if !contains(output, "\n") {
			t.Error("expected pretty-printed json")
		}
	})

	t.Run("stringifies to text", func(t *testing.T) {
		node := NewTransformNode("stringifyText", TransformNodeConfig{
			Transform: TransformStringify,
			InputVar:  "data",
			OutputVar: "result",
			Format:    "text",
		})

		env := NewEnvelope()
		env.SetVar("data", 42)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(string)
		if output != "42" {
			t.Errorf("expected '42', got %q", output)
		}
	})
}

func TestTransformNode_Parse(t *testing.T) {
	t.Run("parses json", func(t *testing.T) {
		node := NewTransformNode("parse", TransformNodeConfig{
			Transform: TransformParse,
			InputVar:  "jsonStr",
			OutputVar: "result",
			Format:    "json",
		})

		env := NewEnvelope()
		env.SetVar("jsonStr", `{"name": "John", "age": 30}`)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["name"] != "John" {
			t.Errorf("expected name='John', got %v", output["name"])
		}
		// JSON numbers are float64
		if output["age"] != float64(30) {
			t.Errorf("expected age=30, got %v", output["age"])
		}
	})

	t.Run("parses json array", func(t *testing.T) {
		node := NewTransformNode("parseArray", TransformNodeConfig{
			Transform: TransformParse,
			InputVar:  "jsonStr",
			OutputVar: "result",
			Format:    "json",
		})

		env := NewEnvelope()
		env.SetVar("jsonStr", `[1, 2, 3]`)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].([]any)
		if len(output) != 3 {
			t.Errorf("expected 3 items, got %d", len(output))
		}
	})

	t.Run("error for invalid json", func(t *testing.T) {
		node := NewTransformNode("parseInvalid", TransformNodeConfig{
			Transform: TransformParse,
			InputVar:  "jsonStr",
			OutputVar: "result",
			Format:    "json",
		})

		env := NewEnvelope()
		env.SetVar("jsonStr", `{invalid}`)

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for invalid json")
		}
	})

	t.Run("error for non-string input", func(t *testing.T) {
		node := NewTransformNode("parseNonString", TransformNodeConfig{
			Transform: TransformParse,
			InputVar:  "data",
			OutputVar: "result",
			Format:    "json",
		})

		env := NewEnvelope()
		env.SetVar("data", 123) // Not a string

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for non-string input")
		}
	})
}

func TestTransformNode_Map(t *testing.T) {
	t.Run("transforms each item", func(t *testing.T) {
		node := NewTransformNode("map", TransformNodeConfig{
			Transform: TransformMap,
			InputVar:  "items",
			OutputVar: "result",
			ItemTransform: &TransformNodeConfig{
				Transform: TransformPick,
				InputVar:  "_item",
				OutputVar: "mapped",
				Fields:    []string{"name"},
			},
		})

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"name": "John", "age": 30},
			{"name": "Jane", "age": 25},
		})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].([]any)
		if len(output) != 2 {
			t.Errorf("expected 2 items, got %d", len(output))
		}

		first := output[0].(map[string]any)
		if first["name"] != "John" {
			t.Errorf("expected first name 'John', got %v", first["name"])
		}
		if _, exists := first["age"]; exists {
			t.Error("age should be picked out")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		node := NewTransformNode("mapCancel", TransformNodeConfig{
			Transform: TransformMap,
			InputVar:  "items",
			OutputVar: "result",
			ItemTransform: &TransformNodeConfig{
				Transform: TransformPick,
				InputVar:  "_item",
				OutputVar: "mapped",
				Fields:    []string{"name"},
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		env := NewEnvelope()
		env.SetVar("items", []map[string]any{
			{"name": "John"},
			{"name": "Jane"},
		})

		_, err := node.Run(ctx, env)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestTransformNode_Custom(t *testing.T) {
	t.Run("custom transformation", func(t *testing.T) {
		node := NewTransformNode("custom", TransformNodeConfig{
			Transform: TransformCustom,
			OutputVar: "result",
			CustomFunc: func(ctx context.Context, env *Envelope) (any, error) {
				name, _ := env.GetVar("name")
				score, _ := env.GetVar("score")
				return map[string]any{
					"greeting": "Hello, " + name.(string),
					"doubled":  score.(float64) * 2,
				}, nil
			},
		})

		env := NewEnvelope()
		env.SetVar("name", "John")
		env.SetVar("score", 0.5)

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := result.Vars["result"].(map[string]any)
		if output["greeting"] != "Hello, John" {
			t.Errorf("expected greeting 'Hello, John', got %v", output["greeting"])
		}
		if output["doubled"] != 1.0 {
			t.Errorf("expected doubled=1.0, got %v", output["doubled"])
		}
	})

	t.Run("custom returns error", func(t *testing.T) {
		node := NewTransformNode("customError", TransformNodeConfig{
			Transform: TransformCustom,
			OutputVar: "result",
			CustomFunc: func(ctx context.Context, env *Envelope) (any, error) {
				return nil, errors.New("custom error")
			},
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error from custom function")
		}
	})

	t.Run("error without custom function", func(t *testing.T) {
		node := NewTransformNode("customNoFunc", TransformNodeConfig{
			Transform: TransformCustom,
			OutputVar: "result",
			// No CustomFunc
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for custom without function")
		}
	})
}

func TestTransformNode_ErrorCases(t *testing.T) {
	t.Run("error without OutputVar", func(t *testing.T) {
		node := NewTransformNode("noOutput", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "data",
			Fields:    []string{"name"},
			// No OutputVar
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "test"})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for missing OutputVar")
		}
	})

	t.Run("error for missing input var", func(t *testing.T) {
		node := NewTransformNode("missingInput", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "nonexistent",
			OutputVar: "result",
			Fields:    []string{"name"},
		})

		env := NewEnvelope()

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for missing input variable")
		}
	})

	t.Run("error for non-map input with pick", func(t *testing.T) {
		node := NewTransformNode("nonMapPick", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "data",
			OutputVar: "result",
			Fields:    []string{"name"},
		})

		env := NewEnvelope()
		env.SetVar("data", "not a map")

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for non-map input with pick")
		}
	})

	t.Run("error for unknown transform type", func(t *testing.T) {
		node := NewTransformNode("unknown", TransformNodeConfig{
			Transform: "unknown_type",
			InputVar:  "data",
			OutputVar: "result",
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{})

		_, err := node.Run(context.Background(), env)
		if err == nil {
			t.Fatal("expected error for unknown transform type")
		}
	})
}

func TestTransformNode_EnvelopeIsolation(t *testing.T) {
	t.Run("original envelope not modified", func(t *testing.T) {
		node := NewTransformNode("isolated", TransformNodeConfig{
			Transform: TransformPick,
			InputVar:  "data",
			OutputVar: "result",
			Fields:    []string{"name"},
		})

		env := NewEnvelope()
		env.SetVar("data", map[string]any{"name": "John", "age": 30})

		result, err := node.Run(context.Background(), env)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Original should not have result
		_, hasResult := env.GetVar("result")
		if hasResult {
			t.Error("original envelope should not have 'result'")
		}

		// Result should have it
		_, hasResult = result.GetVar("result")
		if !hasResult {
			t.Error("result envelope should have 'result'")
		}
	})
}

func TestNestedValueHelpers(t *testing.T) {
	t.Run("getNestedValue", func(t *testing.T) {
		m := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": "deep",
				},
			},
		}

		val, ok := getNestedValue(m, "a.b.c")
		if !ok || val != "deep" {
			t.Errorf("expected 'deep', got %v", val)
		}

		_, ok = getNestedValue(m, "a.b.x")
		if ok {
			t.Error("expected false for missing path")
		}
	})

	t.Run("setNestedValue", func(t *testing.T) {
		m := make(map[string]any)
		setNestedValue(m, "a.b.c", "value")

		val, ok := getNestedValue(m, "a.b.c")
		if !ok || val != "value" {
			t.Errorf("expected 'value', got %v", val)
		}
	})

	t.Run("deleteNestedValue", func(t *testing.T) {
		m := map[string]any{
			"a": map[string]any{
				"b": "value",
				"c": "other",
			},
		}

		deleteNestedValue(m, "a.b")

		a := m["a"].(map[string]any)
		if _, exists := a["b"]; exists {
			t.Error("a.b should be deleted")
		}
		if a["c"] != "other" {
			t.Error("a.c should be preserved")
		}
	})
}

func TestDeepCopy(t *testing.T) {
	t.Run("deepCopyMap", func(t *testing.T) {
		original := map[string]any{
			"a": map[string]any{
				"b": "value",
			},
			"slice": []any{1, 2, 3},
		}

		copied := deepCopyMap(original)

		// Modify copy
		copied["a"].(map[string]any)["b"] = "modified"

		// Original should be unchanged
		if original["a"].(map[string]any)["b"] != "value" {
			t.Error("original should not be modified")
		}
	})
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
