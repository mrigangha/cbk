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
		// Campaigns
		"list_campaigns",
		"get_campaign",
		"create_campaign",
		"update_campaign",
		"delete_campaign",
		"activate_campaign",
		"pause_campaign",
		// Ad sets
		"list_adsets",
		"get_adset",
		"create_adset",
		"update_adset",
		"delete_adset",
		"activate_adset",
		"pause_adset",
		// Ads
		"list_ads",
		"get_ad",
		"create_ad",
		"update_ad",
		"delete_ad",
		"activate_ad",
		"pause_ad",
		// Insights
		"get_campaign_insights",
		"get_adset_insights",
		"get_ad_insights",
		"compare_campaigns",
		// Intelligence
		"get_analytics",
		"get_recommendations",
		"optimize_campaign",
	}

	if len(got) != len(expected) {
		t.Fatalf("registered %d tools, want %d", len(got), len(expected))
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

	// compare_campaigns must carry the ARRAY items schema for campaign_ids
	compare := got["compare_campaigns"]
	prop, ok = compare.Parameters.Properties["campaign_ids"]
	if !ok {
		t.Fatal("compare_campaigns missing campaign_ids property")
	}
	if prop.Type != "ARRAY" || prop.Items == nil || prop.Items.Type != "STRING" {
		t.Fatalf("campaign_ids schema invalid: %+v", prop)
	}
}