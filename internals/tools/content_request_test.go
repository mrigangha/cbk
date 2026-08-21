package tools

import "testing"

func TestContentRequest_IncludesNewTools(t *testing.T) {
	h := NewToolHandler()
	req := ContentRequest(h, []Message{{Role: "user", Content: "hi"}})

	got := map[string]FunctionDeclaration{}
	for _, geminiTool := range req.Tools {
		for _, decl := range geminiTool.FunctionDeclarations {
			got[decl.Name] = decl
		}
	}

	expected := []string{
		"get_campaigns",
		"get_campaign_details",
		"create_campaign",
		"update_campaign",
		"update_campaign_status",
		"delete_campaign",
		"get_adsets",
		"get_ads",
		"get_campaign_insights",
	}

	for _, name := range expected {
		if _, ok := got[name]; !ok {
			t.Fatalf("tool %q missing from Gemini declarations", name)
		}
	}

	// create_campaign must carry the ARRAY items schema for special_ad_categories
	create := got["create_campaign"]
	prop, ok := create.Parameters.Properties["special_ad_categories"]
	if !ok {
		t.Fatal("create_campaign missing special_ad_categories property")
	}
	if prop.Type != "ARRAY" {
		t.Fatalf("special_ad_categories type = %q, want ARRAY", prop.Type)
	}
	if prop.Items == nil {
		t.Fatal("special_ad_categories missing items schema")
	}
	if prop.Items.Type != "STRING" {
		t.Fatalf("items type = %q, want STRING", prop.Items.Type)
	}

	if len(create.Parameters.Required) != 2 ||
		create.Parameters.Required[0] != "name" ||
		create.Parameters.Required[1] != "objective" {
		t.Fatalf("create_campaign required = %v, want [name objective]", create.Parameters.Required)
	}
}