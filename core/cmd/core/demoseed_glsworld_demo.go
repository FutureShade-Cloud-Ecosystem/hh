//go:build demo

// demoseed_glsworld_demo.go, the demo build's side of the demo world seam: the demo seed's
// branches, customers, vendors, products, trucks, drivers, delivery sites and every word a
// screen reads from them become Gable Landscape Supply, the demo tenant (the tenant sheet,
// GLS-TENANT-r2). The default build's side is demoseed_glsworld_default.go, a set of no-ops, so
// a default binary plants the plan exactly as its literals read; demoseed_glsworld_build_test.go
// reads both builds' binaries and proves it.
//
// WHY THE DEMO BUILD ONLY. The plan's literals are the first dealer's UAT world, pinned by the
// default build's plan tests and by the module suites that seed from it, and a database that is
// already seeded would keep its old rows next to new rows written under new words (every insert
// yields on its fixed id). The demo image is the one place the world is built from nothing on a
// fresh database, so the words change there and only there.
//
// THE FOUR PARTS.
//
//  1. glsPrepareWorld moves the two BRANCH rows onto the demo dealer (Point Loma PLM, Julian JUL)
//     before the plan is built. The plan derives every order, purchase order, run and stock move
//     reference from the branch code, so the first pass must already see the final codes.
//  2. init() rewrites the plan's delivery sites (addresses, postal codes, coordinates, phones),
//     the one place the plan holds numbers a text pass cannot reach. The plan's spec tables are
//     package variables, so the demo build's init sets them before any plan is built.
//  3. glsRewritePlan carries every string value of the built plan through the world dictionary,
//     before the rows are applied. The conflict arbiters of a few tables are text numbers
//     (vendor_no, po_no), so the words must be right at insert time, not repaired after.
//  4. glsRenamePlanted carries the rows the MIGRATIONS plant through the same dictionary, inside
//     the apply's transaction, the way the scrub-name pass does for the placeholder names: each
//     update names one column of one row by its own id and writes only when the word changes, so
//     a second run matches nothing.
//
// Nothing here deletes, nothing here touches a row outside the fixed table and column list
// below, and the dictionary is idempotent: the test beside this file applies it twice and
// compares.
package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/jackc/pgx/v5/pgxpool"
)

// glsSite is one invented delivery site in San Diego County: the street line, the town, the
// postal code and the coordinates a stop is planned against, and the phone in the reserved
// 555 0100 to 555 0199 block. Julian's yard delivers in the backcountry (Julian, Wynola, Santa
// Ysabel, Ramona); Point Loma's yard delivers along the coast.
type glsSite struct {
	Phone, Addr, City, Postal string
	Lat, Lng                  float64
}

// glsStopSites are the plan's seventeen delivery stops, by stop key.
var glsStopSites = map[string]glsSite{
	"SP01": {"760-555-0101", "418 Oak Hollow Road", "Julian", "92036", 33.0786, -116.6020},
	"SP02": {"760-555-0102", "77 Kentwood Lane", "Julian", "92036", 33.0811, -116.6047},
	"SP03": {"619-555-0103", "3290 Canon Street", "San Diego", "92106", 32.7386, -117.2290},
	"SP04": {"619-555-0104", "512 Sunset Cliffs Boulevard", "San Diego", "92107", 32.7476, -117.2520},
	"SP05": {"760-555-0105", "1500 Farmers Road", "Julian", "92036", 33.0764, -116.5985},
	"SP06": {"760-555-0106", "88 Wynola Ridge Road", "Julian", "92036", 33.1117, -116.6590},
	"SP07": {"760-555-0107", "233 Cuyamaca Heights Drive", "Julian", "92036", 33.0702, -116.6118},
	"SP08": {"760-555-0108", "640 Pine Hills Road", "Julian", "92036", 33.0853, -116.5791},
	"SP09": {"760-555-0109", "95 Mesa Grande Road", "Santa Ysabel", "92070", 33.1136, -116.6762},
	"SP10": {"760-555-0110", "1201 Highland Valley Road", "Ramona", "92065", 33.0417, -116.8682},
	"SP11": {"760-555-0111", "216 Washington Street", "Ramona", "92065", 33.0370, -116.8700},
	"SP12": {"619-555-0112", "44 Loma Portal Drive", "San Diego", "92106", 32.7510, -117.2150},
	"SP13": {"619-555-0113", "7 Ladera Street", "San Diego", "92106", 32.7320, -117.2330},
	"SP14": {"760-555-0114", "801 Santa Ysabel Road", "Santa Ysabel", "92070", 33.1090, -116.6720},
	"SP15": {"619-555-0115", "35 Fort Stockton Drive", "San Diego", "92103", 32.7580, -117.1850},
	"SP16": {"858-555-0116", "160 La Jolla Shores Drive", "La Jolla", "92037", 32.8570, -117.2570},
	"SP17": {"760-555-0117", "19 Apple Orchard Lane", "Julian", "92036", 33.0770, -116.6001},
}

// glsTodaySites are the two day-keyed delivery stops and the Driver's two day stops, by key.
var glsTodaySites = map[string]glsSite{
	"T3": {"760-555-0131", "77 Eagle Peak Road", "Julian", "92036", 33.0830, -116.6100},
	"T4": {"858-555-0132", "271 Nautilus Street", "La Jolla", "92037", 32.8180, -117.2710},
	"D1": {"760-555-0141", "290 County Yard Road", "Ramona", "92065", 33.0440, -116.8640},
	"D2": {"760-555-0142", "15 Birch Hill Lane", "Julian", "92036", 33.0795, -116.6065},
}

// init moves the plan's delivery sites to the demo dealer's two delivery areas. The spec tables
// are package variables the plan reads when it is built, so the demo build sets them before any
// plan exists. A site key the table does not carry is left alone, which keeps a later stop from
// being silently relocated.
func init() {
	for i := range demoStops {
		if g, ok := glsStopSites[demoStops[i].Key]; ok {
			st := &demoStops[i]
			st.Phone, st.Addr, st.City, st.Postal, st.Lat, st.Lng = g.Phone, g.Addr, g.City, g.Postal, g.Lat, g.Lng
		}
	}
	for i := range demoTodayStops {
		if g, ok := glsTodaySites[demoTodayStops[i].Key]; ok {
			st := &demoTodayStops[i]
			st.Phone, st.Addr, st.City, st.Postal, st.Lat, st.Lng = g.Phone, g.Addr, g.City, g.Postal, g.Lat, g.Lng
		}
	}
	for i := range demoDriverDayStops {
		if g, ok := glsTodaySites[demoDriverDayStops[i].Key]; ok {
			st := &demoDriverDayStops[i]
			st.Phone, st.Addr, st.City, st.Postal, st.Lat, st.Lng = g.Phone, g.Addr, g.City, g.Postal, g.Lat, g.Lng
		}
	}
}

// glsExact is the exact value of a field and the word the demo world reads instead. It is
// consulted first, on a whole value, so a customer's whole name changes without any word of it
// being treated as a token.
var glsExact = map[string]string{
	// -- customers, the plan's ten and the migrations' accounts --
	"Northline Hardscapes Ltd (demo)":      "Summit Ridge Builders",
	"Bay Ridge Landscaping (demo)":         "Ridgeview Outdoor Living",
	"Quinte Paving & Design (demo)":        "Fieldstone Pools and Patios",
	"Harbourview Builders (demo)":          "Harbourview Builders",
	"Cedar Hollow Homes (demo)":            "Cedar Hollow Homes",
	"M. Okafor (demo retail)":              "Homeowner A",
	"D. Lachance (demo retail)":            "Homeowner B",
	"County of Hastings (demo)":            "Valley Municipal Works",
	"Stonebrook Contracting (demo)":        "Stonebrook Contracting",
	"Cash On Demand customer (demo)":       "Cash On Demand customer",
	"Counter Walk-in (demo)":               "Counter walk-in",
	"Riverside Walkway Contracting (demo)": "Riverside Walkway Contracting",
	"Quinte Exteriors (demo)":              "Coastal Edge Exteriors",
	"Limestone City Landscaping (demo)":    "Mesa Verde Landscaping",
	"S. Marchand (demo homeowner)":         "Homeowner C",
	// -- account numbers that carry a first-dealer word --
	"WTY-QUINTE":    "WTY-COASTAL",
	"WTY-LIMESTONE": "WTY-MESA",
	// -- vendors: the plan's four and the migrations' six --
	"Granular supplier (demo)":         "Granite Run Quarry",
	"Paver supplier (demo)":            "Copperline Pavers",
	"Edging and tools supplier (demo)": "Heronfield Stone",
	"Northland Stone Supply (demo)":    "Prairie Bend Soils",
	"Unilock":                          "Alder Creek Paver Works",
	"Techo Bloc":                       "Stonecrest Slab Company",
	"Permacon / Old Castle":            "Cascade Masonry Products",
	"Oaks / Brampton Brick":            "Tidewater Brick and Block",
	"Best Way Stone":                   "Mesa Natural Stone",
	"Banas Stone":                      "Bluff Quarry Stone",
	"V-UNILOCK":                        "V-ALDERCREEK",
	"V-TECHO":                          "V-STONECREST",
	"V-PERMACON":                       "V-CASCADE",
	"V-OAKS":                           "V-TIDEWATER",
	"V-BESTWAY":                        "V-MESA",
	"V-BANAS":                          "V-BLUFF",
	"DEMO-VND-01":                      "DEMO-VND-01",
	// -- products, in the order of the plan's catalogue --
	"Holland Paver, Charcoal (demo)":         "Interlocking Paver 60 mm, Charcoal",
	"Brussels Dimensional, Sandstone (demo)": "Dimensional Paver, Sandstone",
	"Courtstone, Basalt (demo)":              "Large Format Paver, Basalt",
	"Retaining Wall Block 8x18 (demo)":       "Retaining Wall Block 8x18",
	"Step Slab 24x48, Natural (demo)":        "Step Slab 24x48, Natural",
	"HPB Bedding Stone (demo)":               "Paver Bedding Stone",
	"Granular A (demo)":                      "Class 2 Base Rock",
	"Concrete Sand (demo)":                   "Concrete Sand",
	"Black Cedar Mulch (demo)":               "Black Cedar Mulch",
	"Aluminum Edging 8ft (demo)":             "Aluminum Edging 8 ft",
	"Holland Paver volume breaks (demo)":     "Interlocking Paver volume breaks",
	"Granular A volume breaks (demo)":        "Class 2 Base Rock volume breaks",
	"DEMO-PAV-HOLLAND":                       "DEMO-PAV-CHARCOAL",
	"DEMO-PAV-BRUSSELS":                      "DEMO-PAV-SANDSTONE",
	"DEMO-PAV-COURTSTONE":                    "DEMO-PAV-BASALT",
	"DEMO-AGG-HPB":                           "DEMO-AGG-BEDDING",
	"DEMO-AGG-GA":                            "DEMO-AGG-BASE",
	"Unilock Brussels Block":                 "Interlocking Block Paver, Gray",
	"DBS-UL-BRUSSELS":                        "GLS-PAV-BLOCKGRAY",
	"Brown's Concrete":                       "Prairie Bend Soils",
	"Dibbits Yard":                           "Gable Landscape Supply",
	"Brussels Block":                         "Block Paver",
	"Seeded demo catalogue item.":            "Seeded catalogue item.",
	"Seeded demo reorder point.":             "Seeded reorder point.",
	// -- people, by role --
	"Priya Nair (demo)":              "Driver 01",
	"Ted Nurse (demo)":               "Driver 02",
	"Ravi Raman (demo)":              "Driver 03",
	"Owen Sharpe (demo)":             "Driver 04",
	"R. Vance (demo)":                "Site foreman",
	"T. Okonkwo (demo)":              "Yard gate attendant",
	"L. Beaupre (demo)":              "Job site contact",
	"K. Villeneuve (demo)":           "Site foreman",
	"Customer representative (demo)": "Customer representative",
	// -- trucks (the plan's four) --
	"Demo flatbed, 5 ton":   "Truck 01 · flatbed 5 ton",
	"Demo boom truck":       "Truck 02 · boom truck",
	"Demo tandem dump":      "Truck 03 · tandem dump",
	"Demo pickup + trailer": "Truck 04 · pickup and trailer",
}

// glsTokens are the words that are replaced wherever they sit inside a longer value, in order:
// a longer phrase is listed before any shorter one it contains, and no replacement contains a
// word an earlier or later token looks for (the test applies the dictionary twice and
// compares).
var glsTokens = [][2]string{
	{"Hardscape House (demo), Trenton", "Gable Landscape Supply, Point Loma"},
	{"Hardscape House (demo), Kingston", "Gable Landscape Supply, Julian"},
	{"Hardscape House (demo)", "Gable Landscape Supply"},
	{"Quinte Paving & Design (demo)", "Fieldstone Pools and Patios"},
	{"Northline Hardscapes Ltd (demo)", "Summit Ridge Builders"},
	{"Techo-Bloc Blu 60 Slab", "Stonecrest 60 mm Slab"},
	{"Permacon Melville Slab", "Cascade Masonry Slab"},
	{"Oaks Vintage Paver", "Tidewater Tumbled Paver"},
	{"Unilock Il Campo", "Alder Creek Cobble Paver"},
	{"Techo-Bloc Aberdeen Wall", "Stonecrest Wall Unit"},
	{"Best Way Stone Antico Paver", "Mesa Natural Stone Flagstone Paver"},
	{"Best Way Antico", "Mesa Natural Stone Flagstone"},
	{"Oaks Vintage", "Tidewater Tumbled"},
	{"Blu 60", "60 mm"},
	{"Melville", "Cascade"},
	{"Aberdeen", "Stonecrest"},
	{"Il Campo", "Cobble"},
	{"techo-bloc.com", "stonecrest.example"},
	{"Oaks", "Tidewater"},
	{"Techo-Bloc", "Stonecrest Slab Company"},
	{"Techo Bloc", "Stonecrest Slab Company"},
	{"Techo", "Stonecrest"},
	{"Unilock", "Alder Creek Paver Works"},
	{"Permacon", "Cascade Masonry Products"},
	{"Brussels Dimensional", "Dimensional Paver"},
	{"Holland Paver", "Interlocking Paver"},
	{"Brussels Block", "Block Paver"},
	{"Granular A", "Class 2 Base Rock"},
	{"HPB Bedding Stone", "Paver Bedding Stone"},
	{"#yard-trenton", "#yard-point-loma"},
	{"#sales-trenton", "#sales-point-loma"},
	{"#sales-kingston", "#sales-julian"},
	{"yard-trenton", "yard-point-loma"},
	{"sales-trenton", "sales-point-loma"},
	{"sales-kingston", "sales-julian"},
	{"Trenton", "Point Loma"},
	{"Kingston", "Julian"},
	{"On-reserve Dibbits delivery (0%)", "Tax-exempt delivery (0%)"},
	{"@dibbits.example", "@hh-demo.invalid"},
	{"-trenton", "-point-loma"},
	{"-kingston", "-julian"},
	{"Placeholder Way", "Canyon Ridge Way"},
	{"Scrubbed Party 0003", "Contractor Owner"},
	{"\"province\": \"ON\"", "\"province\": \"CA\""},
	{"\"country\": \"CA\"", "\"country\": \"US\""},
	{"Brighton", "La Jolla"},
	{"Dibbits", "Gable Landscape Supply"},
	{"BisTrack", "the old system"},
	{"1150mm", "45 in."},
	{" (demo)", ""},
}

// glsPhone moves the plan's Ontario test numbers to San Diego County numbers in the reserved
// 555 0100 to 555 0199 block. The plan's 0200 block moves down into the 0100 block.
var glsPhone = regexp.MustCompile(`\b613-555-0([12])(\d\d)\b`)

// glsDated drops a closing date from a seeded note: "(the case, <date>)" reads "(the case)". The
// warranty note that carries one names a first dealer's real claim by its day.
var glsDated = regexp.MustCompile(`, \d{4}-\d{2}-\d{2}\)`)

// glsApply carries one value through the dictionary: the exact table first, then the tokens in
// order, then the phone numbers. It is idempotent.
func glsApply(s string) string {
	if v, ok := glsExact[s]; ok {
		return v
	}
	for _, t := range glsTokens {
		if strings.Contains(s, t[0]) {
			s = strings.ReplaceAll(s, t[0], t[1])
		}
	}
	s = glsDated.ReplaceAllString(s, ")")
	return glsPhone.ReplaceAllString(s, "619-555-01$2")
}

// glsRewritePlan carries every string value of the built plan through the dictionary. A JSON
// value (a []byte or a string holding JSON) is a string for this purpose: the dictionary never
// introduces a quote or a backslash, so a document stays a document.
func glsRewritePlan(rows []row) []row {
	for _, r := range rows {
		for i, v := range r.vals {
			switch s := v.(type) {
			case string:
				r.vals[i] = glsApply(s)
			case []byte:
				r.vals[i] = []byte(glsApply(string(s)))
			}
		}
	}
	return rows
}

// glsPrepareWorld moves the two BRANCH rows onto the demo dealer before the plan is built.
func glsPrepareWorld(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("demoseed: begin: %w", err)
	}
	if _, err := demo.ConvergeDealerBranches(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: commit: %w", err)
	}
	return nil
}

// personaEmailAliases maps each persona's migration email to the address the demo populate
// moves it to, so a pass that runs after the populate still finds the three logins.
func personaEmailAliases() map[string]string {
	return map[string]string{
		personaDispatch: "dispatch.demo@hh-demo.invalid",
		personaYard:     "yard.demo@hh-demo.invalid",
		personaDriver:   "driver.demo@hh-demo.invalid",
	}
}

// glsPlanted is one column of one table the migrations plant words in. The id column is the
// table's own uuid; stamped says the table has an updated_at to move; cast is the type the
// column is written back as when it is not plain text.
type glsPlanted struct {
	table, column string
	stamped       bool
	cast          string
}

// glsPlantedColumns is the fixed list of migration-planted columns the dictionary reaches. A
// column is listed only because the scan found a first-dealer word, an Ontario address or a
// person's name in it.
var glsPlantedColumns = []glsPlanted{
	{"customers", "name", true, ""},
	{"customers", "account_no", true, ""},
	{"customers", "billing_address", true, "jsonb"},
	{"vendors", "name", true, ""},
	{"vendors", "vendor_no", true, ""},
	{"products", "name", true, ""},
	{"products", "sku", true, ""},
	{"products", "manufacturer", true, ""},
	{"products", "collection", true, ""},
	{"dispatch_trucks", "name", true, ""},
	{"dispatch_drivers", "name", true, ""},
	{"dispatch_drivers", "phone", true, ""},
	{"delivery_runs", "notes", true, ""},
	{"delivery_freight_raises", "customer_name", false, ""},
	{"permission_approval_requests", "detail", false, ""},
	{"msg_channels", "slug", true, ""},
	{"msg_channels", "name", true, ""},
	{"msg_channels", "topic", true, ""},
	{"msg_messages", "body", false, ""},
	{"order_lines", "description", false, ""},
	{"warranty_claims", "description", true, ""},
	{"warranty_claims", "job_address", true, "jsonb"},
	{"warranty_claim_events", "note", false, ""},
	{"warranty_claim_lines", "product_text", false, ""},
	{"warranty_vendor_profiles", "channel_ref", true, ""},
	{"warranty_vendor_profiles", "notes", true, ""},
	{"tax_status_card_profiles", "name", true, ""},
	{"tax_status_card_profiles", "notes", true, ""},
	{"task_shortages", "note", false, ""},
	{"task_shortages", "resolution", false, ""},
	{"task_shortages", "description", false, ""},
	{"portal_users", "email", true, ""},
	{"portal_users", "name", true, ""},
}

// glsRenamePlanted carries every listed migration-planted column through the dictionary, one
// guarded update per changed value, and returns how many values it changed.
func glsRenamePlanted(ctx context.Context, tx glsTx, now time.Time) (int, error) {
	changed := 0
	for _, p := range glsPlantedColumns {
		n, err := glsRenameColumn(ctx, tx, p, now)
		if err != nil {
			return changed, err
		}
		changed += n
	}
	return changed, nil
}

func glsRenameColumn(ctx context.Context, tx glsTx, p glsPlanted, now time.Time) (int, error) {
	// Table and column come from the fixed list above, never from input.
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT id::text, %s::text FROM %s WHERE %s IS NOT NULL`, p.column, p.table, p.column))
	if err != nil {
		return 0, fmt.Errorf("demoseed: reading %s.%s: %w", p.table, p.column, err)
	}
	type edit struct{ id, to string }
	var edits []edit
	for rows.Next() {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return 0, fmt.Errorf("demoseed: scanning %s.%s: %w", p.table, p.column, err)
		}
		if to := glsApply(v); to != v {
			edits = append(edits, edit{id, to})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].id < edits[j].id })
	for _, e := range edits {
		val := `$1`
		if p.cast != "" {
			val = `$1::` + p.cast
		}
		set, args := p.column+` = `+val, []any{e.to, e.id}
		if p.stamped {
			set, args = set+`, updated_at = $3`, append(args, now)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE id = $2::uuid`, p.table, set), args...); err != nil {
			return 0, fmt.Errorf("demoseed: renaming %s.%s (%s): %w", p.table, p.column, e.id, err)
		}
	}
	return len(edits), nil
}
