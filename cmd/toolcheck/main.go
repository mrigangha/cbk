package main

// Temporary live validator for read-only Meta tools.
// Usage: go run ./cmd/toolcheck

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/mrigangha/cbk/internals/tools"
	_ "modernc.org/sqlite"
)

var failures int

func check(name string, err error, result any) {
	if err != nil {
		failures++
		fmt.Printf("FAIL %-22s %v\n", name, err)
		return
	}
	fmt.Printf("PASS %-22s %s\n", name, summarize(result))
}

func summarize(result any) string {
	switch v := result.(type) {
	case []any:
		return fmt.Sprintf("%d items", len(v))
	case map[string]any:
		if id, ok := v["id"]; ok {
			return fmt.Sprintf("id=%v", id)
		}
		return fmt.Sprintf("%d keys", len(v))
	default:
		return fmt.Sprintf("%T", result)
	}
}

func main() {

	db, err := sql.Open("sqlite", "app.db")
	if err != nil {
		fmt.Println("db open:", err)
		os.Exit(1)
	}
	defer db.Close()

	var accessToken, adAccountID string
	err = db.QueryRow(`
		SELECT access_token, ad_account_id
		FROM meta_ads_accounts ORDER BY updated_at DESC LIMIT 1
	`).Scan(&accessToken, &adAccountID)
	if err != nil {
		fmt.Println("no meta account:", err)
		os.Exit(1)
	}

	ctx := tools.ToolContext{AccessToken: accessToken, AdAccountID: adAccountID}
	fmt.Printf("Validating against act_%s\n\n", adAccountID)

	camps, err := run(ctx, "list_campaigns", map[string]any{})
	check("list_campaigns", err, camps)
	if err != nil || len(camps.([]any)) == 0 {
		done()
	}

	// Walk every campaign until we find one with an ad set so the
	// deeper read-only tools get exercised too.
	var campaignID, adsetID string

	for _, c := range camps.([]any) {
		id := c.(map[string]any)["id"].(string)

		res, err := run(ctx, "get_campaign", map[string]any{"campaign_id": id})
		check("get_campaign "+id, err, res)

		res, err = run(ctx, "get_campaign_insights", map[string]any{"campaign_id": id})
		check("get_campaign_insights "+id, err, res)

		adsets, err := run(ctx, "list_adsets", map[string]any{"campaign_id": id})
		check("list_adsets "+id, err, adsets)
		if err != nil || len(adsets.([]any)) == 0 {
			continue
		}

		campaignID = id
		adsetID = adsets.([]any)[0].(map[string]any)["id"].(string)
		break
	}

	if adsetID == "" {
		fmt.Println("\nNo ad sets found in any campaign; deeper tools skipped.")
		done()
	}
	fmt.Printf("\nValidating ad set %s under campaign %s\n\n", adsetID, campaignID)

	res, err := run(ctx, "get_adset", map[string]any{"adset_id": adsetID})
	check("get_adset", err, res)

	res, err = run(ctx, "get_adset_insights", map[string]any{"adset_id": adsetID})
	check("get_adset_insights", err, res)

	ads, err := run(ctx, "list_ads", map[string]any{"ad_set_id": adsetID})
	check("list_ads", err, ads)
	if err != nil || len(ads.([]any)) == 0 {
		done()
	}
	adID := ads.([]any)[0].(map[string]any)["id"].(string)

	res, err = run(ctx, "get_ad", map[string]any{"ad_id": adID})
	check("get_ad", err, res)

	res, err = run(ctx, "get_ad_insights", map[string]any{"ad_id": adID})
	check("get_ad_insights", err, res)

	done()
}

func run(
	ctx tools.ToolContext,
	name string,
	args map[string]any,
) (any, error) {
	h := tools.NewToolHandler()
	return h.ExecuteTool(ctx, name, args)
}

func done() {
	if failures > 0 {
		fmt.Printf("\n%d FAILURES\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nAll live checks passed.")
	os.Exit(0)
}
