//go:build finworld

package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

var finworldAllowedTowns = []string{
	"Trenton",
	"Belleville",
	"Brighton",
	"Frankford",
	"Stirling",
	"Picton",
	"Napanee",
	"Deseronto",
	"Kingston",
	"Amherstview",
}

var finworldStreetNames = []string{
	"Alder Ridge Way",
	"Bayshore Road",
	"Birch Hollow Lane",
	"Cairnbrook Way",
	"Cedar Creek Drive",
	"Clearwater Crescent",
	"Cobblestone Court",
	"Elmwood Avenue",
	"Fieldstone Trail",
	"Granite Way",
	"Harbourfront Drive",
	"Hickory Ridge Road",
	"Ironwood Lane",
	"Juniper Mill Road",
	"Lakeshore Terrace",
	"Limestone Drive",
	"Maple Crest Avenue",
	"Meadowbrook Road",
	"Millstone Way",
	"Oakridge Boulevard",
	"Orchard Valley Road",
	"Pinehurst Drive",
	"Quarry Ridge Road",
	"Riverbend Way",
	"Sandstone Crescent",
	"Shale Creek Road",
	"Silver Birch Trail",
	"Spruce Hollow Drive",
	"Tamarack Lane",
	"Willowbrook Avenue",
}

var finworldCustomerFirstWords = []string{
	"Alder",
	"Bayshore",
	"Cairnbrook",
	"Cedar",
	"Clearwater",
	"Fieldstone",
	"Granite",
	"Hickory",
	"Ironwood",
	"Limestone",
	"Millstone",
	"Oakridge",
	"Pinehurst",
	"Riverbend",
	"Sandstone",
	"Silver",
}

var finworldCustomerSecondWords = []string{
	"Ridge",
	"Creek",
	"Hollow",
	"Valley",
	"Harbour",
	"Crest",
	"Meadow",
	"Quarry",
	"Point",
	"Grove",
}

var finworldContractorTradeWords = []string{
	"Hardscapes",
	"Landscaping",
	"Masonry",
	"Interlock",
	"Stoneworks",
	"Earthworks",
	"Excavating",
	"Outdoor Living",
	"Paving",
	"Siteworks",
	"Grounds",
	"Contracting",
	"Builders",
	"Patios",
}

var finworldContractorSuffixes = []string{
	"Ltd.",
	"Inc.",
	"Co.",
	"Group",
}

var finworldHouseholdWords = []string{
	"Alder", "Ashford", "Baird", "Barker", "Barton", "Baxter", "Beckett", "Bennett", "Benson", "Bentley",
	"Bishop", "Blackwood", "Blair", "Bolton", "Bowen", "Bradford", "Bradley", "Brampton", "Brennan", "Briggs",
	"Brighton", "Brock", "Brooks", "Bryant", "Burgess", "Burke", "Burnett", "Burton", "Caldwell", "Camden",
	"Cameron", "Campbell", "Carleton", "Carlson", "Carpenter", "Carr", "Carson", "Carter", "Chambers", "Chandler",
	"Chapman", "Clarke", "Clayton", "Clements", "Clifton", "Coleman", "Collins", "Conrad", "Cooper", "Corbett",
	"Craig", "Crawford", "Cromwell", "Cross", "Cunningham", "Curtis", "Dalton", "Daniels", "Davidson", "Dawson",
	"Dixon", "Douglas", "Doyle", "Duncan", "Dunlop", "Dunn", "Easton", "Edwards", "Elliott", "Ellis",
	"Emerson", "Evans", "Fairfax", "Farrell", "Ferguson", "Fielding", "Finley", "Fisher", "Fleming", "Fletcher",
	"Forbes", "Foster", "Fowler", "Fraser", "Freeman", "Fuller", "Gallagher", "Gardner", "Garrett", "Gibson",
	"Gilbert", "Gill", "Goddard", "Gordon", "Graham", "Grant", "Grayson", "Greene", "Griffin", "Hamilton",
	"Hammond", "Hanson", "Harding", "Harper", "Harrison", "Hart", "Hartley", "Harvey", "Hawkins", "Hayes",
	"Henderson", "Hendricks", "Hensley", "Hewitt", "Higgins", "Holden", "Holland", "Holloway", "Holmes", "Holt",
	"Hopkins", "Horton", "Howard", "Hudson", "Hughes", "Hunter", "Ingram", "Irving", "Jansen", "Jennings",
	"Johnston", "Jordan", "Keating", "Keller", "Kemp", "Kendall", "Kennedy", "Kerr", "Kinsley", "Knight",
	"Lambert", "Lancaster", "Langley", "Larson", "Lawson", "Lennox", "Lester", "Lindsey", "Logan", "Lowell",
	"MacLeod", "Maddox", "Manning", "Marlowe", "Marshall", "Mason", "Matthews", "Maxwell", "McAllister", "McBride",
	"McCallum", "McCarthy", "McCormick", "McDonald", "McFarlane", "McGill", "McGregor", "McIntosh", "McKay", "McKenzie",
	"McLean", "McLeod", "McMillan", "McNeil", "Mercer", "Merritt", "Middleton", "Miller", "Mitchell", "Monroe",
}

type finworldCatalogueSpec struct {
	ProductID            string
	SKU                  string
	Name                 string
	CategoryCode         string
	BaseUOM              string
	SellUOM              string
	VendorNo             string
	ListPriceCents       int64
	ContractorPriceCents int64
	StandardCostCents    int64
	SelectionWeight      int
	OpeningStockTRT      int64
	OpeningStockKGN      int64
	ReorderPointTRT      int64
	ReorderPointKGN      int64
	ReorderQtyTRT        int64
	ReorderQtyKGN        int64
}

// finworldCatalogueSpecs lists the 30 DBS- SKUs from core/cmd/core/dibbits_seed.sql with integer cent pricing and weights.
var finworldCatalogueSpecs = []finworldCatalogueSpec{
	{"a7000000-0000-4000-9000-000000000001", "DBS-TB-BLU60", "Techo-Bloc Blu 60 Slab", "SLABSTEP", "PC", "SF", "V-TECHO", 2200, 1804, 1100, 75, 800, 360, 280, 140, 320, 160},
	{"a7000000-0000-4000-9000-000000000002", "DBS-TB-ABERDEEN", "Techo-Bloc Aberdeen Wall", "WALL", "PC", "SF", "V-TECHO", 2800, 2520, 1400, 45, 800, 360, 240, 120, 280, 140},
	{"a7000000-0000-4000-9000-000000000003", "DBS-TB-BOREALIS", "Techo-Bloc Borealis Step", "SLABSTEP", "PC", "EA", "V-TECHO", 9500, 8550, 4800, 35, 400, 180, 120, 60, 140, 70},
	{"a7000000-0000-4000-9000-000000000004", "DBS-TB-MINICRETA", "Techo-Bloc Mini-Creta Wall", "WALL", "PC", "SF", "V-TECHO", 2800, 2520, 1400, 50, 800, 360, 250, 125, 300, 150},
	{"a7000000-0000-4000-9000-000000000005", "DBS-UL-BRUSSELS", "Unilock Brussels Block", "PAVER", "PC", "SF", "V-UNILOCK", 1800, 1476, 900, 90, 950, 420, 320, 160, 380, 190},
	{"a7000000-0000-4000-9000-000000000006", "DBS-UL-ILCAMPO", "Unilock Il Campo Paver", "PAVER", "PC", "SF", "V-UNILOCK", 2200, 1804, 1100, 80, 850, 400, 300, 150, 350, 175},
	{"a7000000-0000-4000-9000-000000000007", "DBS-UL-SERIES3000", "Unilock Series 3000", "PAVER", "PC", "SF", "V-UNILOCK", 2200, 1804, 1100, 75, 850, 400, 280, 140, 340, 170},
	{"a7000000-0000-4000-9000-000000000008", "DBS-PM-MELVILLE", "Permacon Melville Slab", "SLABSTEP", "PC", "SF", "V-PERMACON", 2200, 1804, 1100, 70, 800, 360, 260, 130, 320, 160},
	{"a7000000-0000-4000-9000-000000000009", "DBS-PM-LAFITT", "Permacon Lafitt Paver", "PAVER", "PC", "SF", "V-PERMACON", 2200, 1804, 1100, 75, 850, 380, 280, 140, 340, 170},
	{"a7000000-0000-4000-9000-000000000010", "DBS-PM-MAGNUMSTONE", "Permacon Magnumstone Wall", "WALL", "PC", "SF", "V-PERMACON", 2800, 2520, 1400, 40, 600, 280, 200, 100, 240, 120},
	{"a7000000-0000-4000-9000-000000000011", "DBS-OAKS-VINTAGE", "Oaks Vintage Paver", "PAVER", "PC", "SF", "V-OAKS", 2200, 1804, 1100, 70, 800, 360, 260, 130, 320, 160},
	{"a7000000-0000-4000-9000-000000000012", "DBS-OAKS-PROMA", "Oaks Proma Wall", "WALL", "PC", "SF", "V-OAKS", 2800, 2520, 1400, 45, 700, 320, 220, 110, 260, 130},
	{"a7000000-0000-4000-9000-000000000013", "DBS-BWS-ANTICO", "Best Way Stone Antico Paver", "PAVER", "PC", "SF", "V-BESTWAY", 2200, 1804, 1100, 65, 800, 360, 260, 130, 300, 150},
	{"a7000000-0000-4000-9000-000000000014", "DBS-BWS-TREVISTA", "Best Way Stone Trevista Slab", "SLABSTEP", "PC", "SF", "V-BESTWAY", 2200, 1804, 1100, 60, 750, 340, 240, 120, 280, 140},
	{"a7000000-0000-4000-9000-000000000015", "DBS-BANAS-COPING", "Banas Sandstone Coping", "SLABSTEP", "PC", "LF", "V-BANAS", 3200, 2880, 1600, 40, 600, 280, 200, 100, 240, 120},
	{"a7000000-0000-4000-9000-000000000016", "DBS-BANAS-STEP", "Banas Natural Step", "SLABSTEP", "PC", "EA", "V-BANAS", 11000, 9900, 5500, 30, 350, 160, 110, 55, 130, 65},
	{"a7000000-0000-4000-9000-000000000017", "DBS-PORCEA-2424", "Porcea Porcelain Paver 24x24", "PAVER", "PC", "SF", "V-PORCEA", 2600, 2132, 1300, 55, 700, 320, 220, 110, 260, 130},
	{"a7000000-0000-4000-9000-000000000018", "DBS-PORCEA-PLANK", "Porcea Porcelain Plank", "PAVER", "PC", "SF", "V-PORCEA", 2600, 2132, 1300, 50, 700, 320, 220, 110, 260, 130},
	{"a7000000-0000-4000-9000-000000000019", "DBS-AGG-HPB", "HPB Clear Stone", "BULK", "T", "T", "V-BROWNS", 6500, 5850, 3500, 85, 300, 140, 100, 50, 120, 60},
	{"a7000000-0000-4000-9000-000000000020", "DBS-AGG-SCREEN", "Limestone Screenings", "BULK", "T", "T", "V-BROWNS", 4800, 4320, 2600, 80, 300, 140, 100, 50, 120, 60},
	{"a7000000-0000-4000-9000-000000000021", "DBS-AGG-SAND", "Concrete Sand", "BULK", "T", "T", "V-BROWNS", 5200, 4680, 2800, 80, 300, 140, 100, 50, 120, 60},
	{"a7000000-0000-4000-9000-000000000022", "DBS-AGG-34CLEAR", "3/4 Clear Stone", "BULK", "T", "T", "V-BROWNS", 6000, 5400, 3200, 85, 300, 140, 100, 50, 120, 60},
	{"a7000000-0000-4000-9000-000000000023", "DBS-GATOR-SAND", "Alliance Gator Polymeric Sand", "ACCESSORY", "BAG", "BAG", "V-GATOR", 3400, 3128, 1700, 85, 500, 240, 160, 80, 200, 100},
	{"a7000000-0000-4000-9000-000000000024", "DBS-SIKA-SEALER", "Sika Paver Sealer 4L", "ACCESSORY", "EA", "EA", "V-SIKA", 6800, 6256, 3400, 12, 180, 80, 50, 25, 60, 30},
	{"a7000000-0000-4000-9000-000000000025", "DBS-INLITE-LED", "In-Lite LED Path Light", "ACCESSORY", "EA", "EA", "V-INLITE", 14500, 13340, 7200, 10, 160, 75, 45, 22, 55, 28},
	{"a7000000-0000-4000-9000-000000000026", "DBS-RYMAR-TURF", "Rymar Artificial Turf 40mm", "ACCESSORY", "SF", "SF", "V-RYMAR", 900, 828, 450, 10, 600, 280, 180, 90, 220, 110},
	{"a7000000-0000-4000-9000-000000000027", "DBS-GEO-FABRIC", "Geotextile Fabric Roll", "ACCESSORY", "EA", "EA", "V-SOLENO", 12500, 11500, 6200, 15, 200, 90, 60, 30, 75, 38},
	{"a7000000-0000-4000-9000-000000000028", "DBS-TB-HYDR", "Techo-Bloc Hydr Eco Paver", "PAVER", "PC", "SF", "V-TECHO", 2400, 1968, 1200, 65, 800, 360, 260, 130, 300, 150},
	{"a7000000-0000-4000-9000-000000000029", "DBS-UL-THORNBURY", "Unilock Thornbury Wall", "WALL", "PC", "SF", "V-UNILOCK", 2800, 2520, 1400, 45, 700, 320, 220, 110, 260, 130},
	{"a7000000-0000-4000-9000-000000000030", "DBS-PM-BORANJA", "Permacon Boranja Slab", "SLABSTEP", "PC", "SF", "V-PERMACON", 2200, 1804, 1100, 65, 800, 360, 260, 130, 300, 150},
}

// finworldHSTOn mirrors tax.hstOn in core/internal/tax/model.go:355 (half-up integer rounding).
func finworldHSTOn(subtotalCents int64, rateBps int) int64 {
	if subtotalCents <= 0 || rateBps <= 0 {
		return 0
	}
	return (subtotalCents*int64(rateBps) + FinWorldTaxRoundBase/2) / FinWorldTaxRoundBase
}

func finworldVerifyCatalogueAgainstSeedSQL() error {
	raw, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		return fmt.Errorf("finworld: read embedded dibbits_seed.sql: %w", err)
	}
	text := string(raw)
	for _, spec := range finworldCatalogueSpecs {
		needle := "'" + spec.SKU + "'"
		if !strings.Contains(text, needle) {
			return fmt.Errorf("finworld: SKU %s not found in embedded dibbits_seed.sql", spec.SKU)
		}
	}
	return nil
}

func finworldSeasonForMonth(month int) (FinWorldSeasonBand, int) {
	switch month {
	case 12, 1, 2:
		return FinWorldSeasonWinter, FinWorldSeasonWinterBps
	case 3, 4, 10, 11:
		return FinWorldSeasonShoulder, FinWorldSeasonShoulderBps
	default:
		return FinWorldSeasonPeak, FinWorldSeasonPeakBps
	}
}

func finworldScalePeakCount(peakCount, seasonBps int) int {
	scaled := (peakCount*seasonBps + 5000) / 10000
	if scaled < 1 {
		scaled = 1
	}
	return scaled
}

func finworldDistributeCount(total, buckets int, rng *rand.Rand) []int {
	out := make([]int, buckets)
	if buckets <= 0 || total <= 0 {
		return out
	}
	base := total / buckets
	rem := total % buckets
	for i := 0; i < buckets; i++ {
		out[i] = base
	}
	// Pick rem distinct buckets deterministically using a Fisher-Yates index permutation.
	perm := make([]int, buckets)
	for i := range perm {
		perm[i] = i
	}
	for i := buckets - 1; i > 0; i-- {
		j := rng.IntN(i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	for i := 0; i < rem; i++ {
		out[perm[i]]++
	}
	return out
}

type finworldReturnableLine struct {
	SaleKey        string
	InvoiceKey     string
	SaleDate       string
	CustomerKey    string
	PaymentMethod  string
	TaxReasonCode  string
	TaxRateBps     int
	LineNo         int
	LineKey        string
	SKU            string
	UOM            string
	OrigQty        int64
	RemainingQty   int64
	UnitPriceCents int64
	UnitCostCents  int64
	LineTaxCents   int64
}

type finworldOpenInvoice struct {
	InvoiceKey         string
	SaleKey            string
	CustomerKey        string
	BranchCode         string
	IssueDate          string
	DueDate            string
	MonthIndex         int
	CreatedGlobalDay   int
	ScheduledPayDay    int // -1 if intentionally left open for aging
	OriginalTotalCents int64
	OpenBalanceCents   int64
	Bounced            bool
}

type finworldScheduledBounce struct {
	ReceiptKey   string
	PaymentKey   string
	ReceiptDate  string
	BranchCode   string
	CustomerKey  string
	AmountCents  int64
	InvoiceKeys  []string
	ScheduledDay int
	Emitted      bool
}

// finworldDealerLegalSuffix is the legal form suffix the world derives onto the caller's dealer name.
const finworldDealerLegalSuffix = " Ltd."

// FinWorldPlan builds the deterministic 12-month world plan for (seed, anchor). The dealer's display name
// is an INPUT: the caller reads the deploy's setting (HH_WEB_DEALER_NAME) and hands it over, so the world
// code carries no dealer name literal of its own. The legal entity name is derived from that input.
func FinWorldPlan(seed uint64, anchor YearMonth, dealerName string) (*FinWorldPlanT, error) {
	if !anchor.Valid() {
		return nil, fmt.Errorf("finworld: invalid anchor YearMonth %+v", anchor)
	}
	dealerName = strings.TrimSpace(dealerName)
	if dealerName == "" {
		return nil, fmt.Errorf("finworld: a dealer name is required (the caller reads the deploy's setting)")
	}
	if err := finworldVerifyCatalogueAgainstSeedSQL(); err != nil {
		return nil, err
	}

	hz := houseday.DefaultZone()
	houseZone := hz.Name()
	loc := hz.Location()

	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	startMonth := anchor.AddMonths(-11)

	plan := &FinWorldPlanT{
		Seed:                  seed,
		Anchor:                anchor,
		StartMonth:            startMonth,
		HouseZone:             houseZone,
		StatutoryHolidaysNote: "core/internal/platform/houseday defines no statutory holidays; Sundays are excluded and no statutory holidays are excluded",
		TaxRoundingNote:       "per-line HST computed via finworldHSTOn mirroring tax.hstOn in core/internal/tax/model.go:355: (subtotalCents*int64(rateBps) + 5000) / 10000",
		CatalogueSourceNote:   "30 DBS- SKUs verified at plan construction against the embedded catalogue seed SQL (seedFS) and paired with finworldCatalogueSpecs",
	}

	// 1. Build the dealer. Its display name is the caller's input and its legal entity name is derived
	// from that input, so the world code holds no dealer name literal.
	dealerStreet := finworldStreetNames[rng.IntN(len(finworldStreetNames))]
	plan.Dealer = FinWorldDealer{
		Key:           fmt.Sprintf("dealer-s%d", seed),
		Name:          dealerName,
		LegalName:     dealerName + finworldDealerLegalSuffix,
		StreetAddress: fmt.Sprintf("%d %s", 100+rng.IntN(800), dealerStreet),
		Town:          "Trenton",
		Province:      "ON",
		PostalCode:    "K8V 1A1",
		Phone:         "613-555-0100",
		SupportEmail:  fmt.Sprintf("desk-s%d@example.invalid", seed),
	}

	// 2. Build the two seeded branches (Trenton and Kingston).
	trtStreet := finworldStreetNames[rng.IntN(len(finworldStreetNames))]
	kgnStreet := finworldStreetNames[rng.IntN(len(finworldStreetNames))]
	plan.Branches = []FinWorldBranch{
		{
			ID:             FinWorldBranchIDTRT,
			Code:           FinWorldBranchCodeTRT,
			Name:           "Trenton Yard",
			StreetAddress:  fmt.Sprintf("%d %s", 200+rng.IntN(600), trtStreet),
			Town:           "Trenton",
			Province:       "ON",
			PostalCode:     "K8V 2B4",
			VolumeRatioBps: FinWorldBranchRatioTRT,
			TillKeys: []string{
				fmt.Sprintf("till-s%d-trt-1", seed),
				fmt.Sprintf("till-s%d-trt-2", seed),
			},
		},
		{
			ID:             FinWorldBranchIDKGN,
			Code:           FinWorldBranchCodeKGN,
			Name:           "Kingston Yard",
			StreetAddress:  fmt.Sprintf("%d %s", 300+rng.IntN(600), kgnStreet),
			Town:           "Kingston",
			Province:       "ON",
			PostalCode:     "K7L 3C8",
			VolumeRatioBps: FinWorldBranchRatioKGN,
			TillKeys: []string{
				fmt.Sprintf("till-s%d-kgn-1", seed),
				fmt.Sprintf("till-s%d-kgn-2", seed),
			},
		},
	}

	// 3. Build 70 invented contractors (first 15 are top Pareto contractors).
	topParetoLimits := []int64{7500000, 12000000, 20000000}
	stdContractorLimits := []int64{3000000, 4000000, 5000000, 7500000}
	contractorTerms := []int{30, 30, 30, 45, 60}
	contactRoles := []string{"Accounts Desk", "Purchasing Desk", "Estimating Desk", "Operations Desk"}

	plan.Contractors = make([]FinWorldCustomer, 70)
	for i := 0; i < 70; i++ {
		w1 := finworldCustomerFirstWords[(i+rng.IntN(len(finworldCustomerFirstWords)))%len(finworldCustomerFirstWords)]
		w2 := finworldCustomerSecondWords[(i/2+rng.IntN(len(finworldCustomerSecondWords)))%len(finworldCustomerSecondWords)]
		trade := finworldContractorTradeWords[(i+rng.IntN(len(finworldContractorTradeWords)))%len(finworldContractorTradeWords)]
		suffix := finworldContractorSuffixes[i%len(finworldContractorSuffixes)]
		town := finworldAllowedTowns[(i+rng.IntN(len(finworldAllowedTowns)))%len(finworldAllowedTowns)]
		street := finworldStreetNames[(i+rng.IntN(len(finworldStreetNames)))%len(finworldStreetNames)]
		isTop := i < 15
		var limit int64
		if isTop {
			limit = topParetoLimits[rng.IntN(len(topParetoLimits))]
		} else {
			limit = stdContractorLimits[rng.IntN(len(stdContractorLimits))]
		}
		terms := contractorTerms[rng.IntN(len(contractorTerms))]
		homeBranch := FinWorldBranchCodeTRT
		if town == "Kingston" || town == "Amherstview" || town == "Napanee" {
			homeBranch = FinWorldBranchCodeKGN
		}
		plan.Contractors[i] = FinWorldCustomer{
			Key:               fmt.Sprintf("cust-s%d-c%03d", seed, i+1),
			AccountNo:         fmt.Sprintf("FW-C%03d", i+1),
			CustomerType:      "CONTRACTOR",
			IsTopPareto:       isTop,
			Name:              fmt.Sprintf("%s %s %s %s %d", w1, w2, trade, suffix, i+1),
			ContactName:       contactRoles[i%len(contactRoles)],
			Email:             fmt.Sprintf("contractor-%03d-s%d@example.invalid", i+1, seed),
			Phone:             fmt.Sprintf("613-555-%04d", 1001+i),
			StreetAddress:     fmt.Sprintf("%d %s", 10+((i*17+rng.IntN(90))%890), street),
			Town:              town,
			Province:          "ON",
			PostalCode:        fmt.Sprintf("K8N %dB%d", (i%9)+1, ((i+3)%9)+1),
			HomeBranchCode:    homeBranch,
			CreditLimitCents:  limit,
			PaymentTermsDays:  terms,
			TaxReliefEligible: false,
		}
	}

	// 4. Build 180 invented homeowners ("The <Word> Household").
	plan.Homeowners = make([]FinWorldCustomer, 180)
	for i := 0; i < 180; i++ {
		word := finworldHouseholdWords[i%len(finworldHouseholdWords)]
		town := finworldAllowedTowns[(i+rng.IntN(len(finworldAllowedTowns)))%len(finworldAllowedTowns)]
		street := finworldStreetNames[(i+rng.IntN(len(finworldStreetNames)))%len(finworldStreetNames)]
		homeBranch := FinWorldBranchCodeTRT
		if town == "Kingston" || town == "Amherstview" || town == "Napanee" {
			homeBranch = FinWorldBranchCodeKGN
		}
		plan.Homeowners[i] = FinWorldCustomer{
			Key:               fmt.Sprintf("cust-s%d-h%03d", seed, i+1),
			AccountNo:         fmt.Sprintf("FW-H%03d", i+1),
			CustomerType:      "RETAIL",
			IsTopPareto:       false,
			Name:              fmt.Sprintf("The %s Household", word),
			ContactName:       "Household Member",
			Email:             fmt.Sprintf("household-%03d-s%d@example.invalid", i+1, seed),
			Phone:             fmt.Sprintf("613-555-%04d", 2001+i),
			StreetAddress:     fmt.Sprintf("%d %s", 12+((i*13+rng.IntN(80))%780), street),
			Town:              town,
			Province:          "ON",
			PostalCode:        fmt.Sprintf("K8P %dC%d", (i%9)+1, ((i+5)%9)+1),
			HomeBranchCode:    homeBranch,
			CreditLimitCents:  0,
			PaymentTermsDays:  0,
			TaxReliefEligible: i < 12,
		}
	}

	// 5. Build the 30 catalogue products and weighted picker table.
	plan.Products = make([]FinWorldProduct, len(finworldCatalogueSpecs))
	totalProductWeight := 0
	for i, spec := range finworldCatalogueSpecs {
		plan.Products[i] = FinWorldProduct{
			ProductID:            spec.ProductID,
			SKU:                  spec.SKU,
			Name:                 spec.Name,
			CategoryCode:         spec.CategoryCode,
			BaseUOM:              spec.BaseUOM,
			SellUOM:              spec.SellUOM,
			VendorNo:             spec.VendorNo,
			ListPriceCents:       spec.ListPriceCents,
			ContractorPriceCents: spec.ContractorPriceCents,
			StandardCostCents:    spec.StandardCostCents,
			SelectionWeight:      spec.SelectionWeight,
			OpeningStockTRT:      spec.OpeningStockTRT,
			OpeningStockKGN:      spec.OpeningStockKGN,
			ReorderPointTRT:      spec.ReorderPointTRT,
			ReorderPointKGN:      spec.ReorderPointKGN,
			ReorderQtyTRT:        spec.ReorderQtyTRT,
			ReorderQtyKGN:        spec.ReorderQtyKGN,
		}
		totalProductWeight += spec.SelectionWeight
	}

	pickWeightedProductIndex := func() int {
		draw := rng.IntN(totalProductWeight)
		acc := 0
		for i, p := range plan.Products {
			acc += p.SelectionWeight
			if draw < acc {
				return i
			}
		}
		return len(plan.Products) - 1
	}

	// 6. Build the 12 months and their non-Sunday trading days in America/Toronto.
	plan.Months = make([]FinWorldMonthPlan, 12)
	totalTradingDays := 0
	for mIdx := 0; mIdx < 12; mIdx++ {
		ym := startMonth.AddMonths(mIdx)
		band, factorBps := finworldSeasonForMonth(ym.Month)
		isYearEnd := ym.Month == 12
		if isYearEnd {
			plan.FiscalYearEndMonth = ym
		}

		firstOfNext := ym.AddMonths(1)
		endOfMonth := time.Date(firstOfNext.Year, time.Month(firstOfNext.Month), 1, 0, 0, 0, 0, loc).AddDate(0, 0, -1)
		daysInMonth := endOfMonth.Day()

		var tdays []FinWorldTradingDay
		for d := 1; d <= daysInMonth; d++ {
			t := time.Date(ym.Year, time.Month(ym.Month), d, 12, 0, 0, 0, loc)
			if t.Weekday() == time.Sunday {
				continue
			}
			tdays = append(tdays, FinWorldTradingDay{
				Date:            t.Format("2006-01-02"),
				DayOfMonth:      d,
				Weekday:         t.Weekday().String(),
				TradingDayIndex: totalTradingDays,
			})
			totalTradingDays++
		}
		if len(tdays) > 0 {
			tdays[len(tdays)-1].IsLastOfMonth = true
		}

		plan.Months[mIdx] = FinWorldMonthPlan{
			MonthIndex:      mIdx,
			YearMonth:       ym,
			SeasonBand:      band,
			SeasonFactorBps: factorBps,
			IsClosed:        mIdx < 11,
			IsFiscalYearEnd: isYearEnd,
			FirstTradingDay: tdays[0].Date,
			LastTradingDay:  tdays[len(tdays)-1].Date,
			TradingDays:     tdays,
			Summary: FinWorldMonthSummary{
				YearMonth:       ym,
				SeasonBand:      band,
				SeasonFactorBps: factorBps,
				TradingDayCount: len(tdays),
				ByBranch: map[string]FinWorldBranchSummary{
					FinWorldBranchCodeTRT: {BranchCode: FinWorldBranchCodeTRT},
					FinWorldBranchCodeKGN: {BranchCode: FinWorldBranchCodeKGN},
				},
			},
		}
	}

	// Track branch on-hand stock across the 12 months.
	stockTRT := make([]int64, len(plan.Products))
	stockKGN := make([]int64, len(plan.Products))
	for i, p := range plan.Products {
		stockTRT[i] = p.OpeningStockTRT
		stockKGN[i] = p.OpeningStockKGN
	}

	// Track returnable sale lines, open AR invoices, scheduled receipts, and scheduled NSF bounces.
	returnableByBranch := map[string][]*finworldReturnableLine{
		FinWorldBranchCodeTRT: nil,
		FinWorldBranchCodeKGN: nil,
	}
	var allOpenInvoices []*finworldOpenInvoice
	invoicesByPayDay := make(map[int][]*finworldOpenInvoice)
	var scheduledBounces []*finworldScheduledBounce

	// Counters for deterministic keys and global sequence.
	globalSeq := 0
	saleSeq := 0
	orderSeq := 0
	invoiceSeq := 0
	paymentSeq := 0
	returnSeq := 0
	creditMemoSeq := 0
	receiptSeq := 0
	bounceSeq := 0
	tillSessionSeq := 0
	tillMoveSeq := 0
	lodgementSeq := 0
	cardSettleSeq := 0
	purchaseReceiptSeq := 0
	poSeq := 0
	vendorBillSeq := 0
	transferSeq := 0
	journalSeq := 0
	bankStmtSeq := 0
	periodCloseSeq := 0
	yearCloseSeq := 0
	accountInvoiceCounter := 0

	// Track bank balance and prior month's deposits in transit / outstanding cheques.
	var runningBankBalanceCents int64 = 18500000 // $185,000.00 opening operating bank balance
	var priorTransitLodgements []*FinWorldLodgementEvent
	var priorOutstandingBills []*FinWorldVendorBillEvent

	// Helper to format an America/Toronto RFC3339 timestamp on dateStr at hour:min:sec.
	formatOccurredAt := func(dateStr string, hour, minute, second int) string {
		parsed, _ := time.ParseInLocation("2006-01-02", dateStr, loc)
		ts := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), hour, minute, second, 0, loc)
		return ts.Format(time.RFC3339)
	}

	// 7. Walk month by month and trading day by trading day.
	for mIdx := range plan.Months {
		m := &plan.Months[mIdx]
		numDays := len(m.TradingDays)
		factor := m.SeasonFactorBps

		// Monthly target counts for TRT and KGN (KGN = 0.5 of TRT).
		trtCounterTarget := finworldScalePeakCount(600, factor)
		kgnCounterTarget := finworldScalePeakCount(300, factor)

		trtAccountTarget := finworldScalePeakCount(260, factor)
		kgnAccountTarget := finworldScalePeakCount(130, factor)

		trtDeliveredTarget := finworldScalePeakCount(110, factor)
		kgnDeliveredTarget := finworldScalePeakCount(55, factor)

		trtCollectCODTarget := finworldScalePeakCount(40, factor)
		kgnCollectCODTarget := finworldScalePeakCount(20, factor)

		trtCreditMemoTarget := finworldScalePeakCount(8, factor)
		kgnCreditMemoTarget := finworldScalePeakCount(4, factor)

		trtPurchaseTarget := finworldScalePeakCount(40, factor)
		kgnPurchaseTarget := finworldScalePeakCount(20, factor)

		monthTransferTarget := finworldScalePeakCount(6, factor)

		// Per-day distributions across the month's trading days.
		trtCounterPerDay := finworldDistributeCount(trtCounterTarget, numDays, rng)
		kgnCounterPerDay := finworldDistributeCount(kgnCounterTarget, numDays, rng)
		trtAccountPerDay := finworldDistributeCount(trtAccountTarget, numDays, rng)
		kgnAccountPerDay := finworldDistributeCount(kgnAccountTarget, numDays, rng)
		trtDeliveredPerDay := finworldDistributeCount(trtDeliveredTarget, numDays, rng)
		kgnDeliveredPerDay := finworldDistributeCount(kgnDeliveredTarget, numDays, rng)
		trtCollectCODPerDay := finworldDistributeCount(trtCollectCODTarget, numDays, rng)
		kgnCollectCODPerDay := finworldDistributeCount(kgnCollectCODTarget, numDays, rng)
		trtCreditMemoPerDay := finworldDistributeCount(trtCreditMemoTarget, numDays, rng)
		kgnCreditMemoPerDay := finworldDistributeCount(kgnCreditMemoTarget, numDays, rng)
		trtPurchasePerDay := finworldDistributeCount(trtPurchaseTarget, numDays, rng)
		kgnPurchasePerDay := finworldDistributeCount(kgnPurchaseTarget, numDays, rng)
		transfersPerDay := finworldDistributeCount(monthTransferTarget, numDays, rng)

		monthSalesLinesByBranch := map[string]int{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}
		monthReturnLinesByBranch := map[string]int{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}
		bouncesEmittedInMonth := 0
		targetBouncesInMonth := 1
		if m.SeasonBand == FinWorldSeasonPeak {
			targetBouncesInMonth = 2
		}

		var monthLodgements []*FinWorldLodgementEvent
		var monthCardSettlements []*FinWorldCardSettlementEvent
		var monthEFTReceipts []*FinWorldAccountReceiptEvent
		var monthVendorBills []*FinWorldVendorBillEvent
		var monthNSFBounces []*FinWorldNSFBounceEvent

		for dIdx := 0; dIdx < numDays; dIdx++ {
			td := &m.TradingDays[dIdx]
			daySeq := 0
			globalDay := td.TradingDayIndex

			appendEvent := func(branchCode string, kind FinWorldEventKind, hour, min, sec int) *FinWorldEvent {
				globalSeq++
				daySeq++
				ev := FinWorldEvent{
					GlobalSeq:  globalSeq,
					DaySeq:     daySeq,
					EventKey:   fmt.Sprintf("ev-s%d-%06d", seed, globalSeq),
					Date:       td.Date,
					OccurredAt: formatOccurredAt(td.Date, hour, min, sec),
					BranchCode: branchCode,
					Kind:       kind,
				}
				td.Events = append(td.Events, ev)
				return &td.Events[len(td.Events)-1]
			}

			// 7a. Morning: open 2 tills per branch.
			sessionKeyByBranchTill := make(map[string][2]string)
			tillCashNetBySession := make(map[string]int64)
			const openingFloatCents int64 = 20000 // $200.00 float

			for bIdx, br := range plan.Branches {
				var pair [2]string
				for tIdx := 0; tIdx < 2; tIdx++ {
					tillSessionSeq++
					sKey := fmt.Sprintf("tillsess-s%d-%05d", seed, tillSessionSeq)
					pair[tIdx] = sKey
					tillCashNetBySession[sKey] = openingFloatCents

					ev := appendEvent(br.Code, FinWorldEventTillOpen, 7, bIdx*3+tIdx*2, 0)
					ev.TillOpen = &FinWorldTillOpenEvent{
						SessionKey:        sKey,
						TillKey:           br.TillKeys[tIdx],
						BranchCode:        br.Code,
						OpeningFloatCents: openingFloatCents,
					}
					bs := m.Summary.ByBranch[br.Code]
					bs.TillOpens++
					m.Summary.ByBranch[br.Code] = bs
					m.Summary.TillOpens++
				}
				sessionKeyByBranchTill[br.Code] = pair
			}

			// 7b. Occasional till paid_in / paid_out movements on ~4 trading days per month per branch.
			if dIdx%6 == 2 {
				for _, br := range plan.Branches {
					tillMoveSeq++
					sKey := sessionKeyByBranchTill[br.Code][0]
					dir := "PAID_OUT"
					var amt int64 = 2500
					reason := "Yard petty cash supplies"
					if (mIdx+dIdx)%2 == 0 {
						dir = "PAID_IN"
						amt = 3000
						reason = "Coin roll float top-up"
						tillCashNetBySession[sKey] += amt
					} else {
						tillCashNetBySession[sKey] -= amt
					}
					ev := appendEvent(br.Code, FinWorldEventTillPaidInOut, 8, 15, 0)
					ev.TillPaidInOut = &FinWorldTillPaidInOutEvent{
						MovementKey: fmt.Sprintf("tillmv-s%d-%04d", seed, tillMoveSeq),
						SessionKey:  sKey,
						TillKey:     br.TillKeys[0],
						BranchCode:  br.Code,
						Direction:   dir,
						AmountCents: amt,
						Reason:      reason,
					}
					bs := m.Summary.ByBranch[br.Code]
					bs.TillPaidInOuts++
					m.Summary.ByBranch[br.Code] = bs
					m.Summary.TillPaidInOuts++
				}
			}

			// Track daily card sales/returns/receipts and cheque receipts per branch for EOD settlement and lodgement.
			dayCardSaleKeys := map[string][]string{FinWorldBranchCodeTRT: nil, FinWorldBranchCodeKGN: nil}
			dayCardReturnKeys := map[string][]string{FinWorldBranchCodeTRT: nil, FinWorldBranchCodeKGN: nil}
			dayCardReceiptKeys := map[string][]string{FinWorldBranchCodeTRT: nil, FinWorldBranchCodeKGN: nil}
			dayGrossCardCents := map[string]int64{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}
			dayRefundCardCents := map[string]int64{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}
			dayChequeReceiptKeys := map[string][]string{FinWorldBranchCodeTRT: nil, FinWorldBranchCodeKGN: nil}
			dayChequeCents := map[string]int64{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}

			// Track reorder crossings on this day per branch.
			reordersDoneToday := map[string]int{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}
			dayBranchCounterIdx := map[string]int{FinWorldBranchCodeTRT: 0, FinWorldBranchCodeKGN: 0}

			emitPurchaseAndBill := func(branchCode string, skuIdx int, triggeredByReorder bool, hour, min int) {
				prod := plan.Products[skuIdx]
				purchaseReceiptSeq++
				poSeq++
				vendorBillSeq++

				prKey := fmt.Sprintf("pr-s%d-%05d", seed, purchaseReceiptSeq)
				poKey := fmt.Sprintf("po-s%d-%05d", seed, poSeq)
				vbKey := fmt.Sprintf("vbill-s%d-%05d", seed, vendorBillSeq)

				var stockSlice []int64
				var reorderPt, reorderQty int64
				if branchCode == FinWorldBranchCodeTRT {
					stockSlice = stockTRT
					reorderPt = prod.ReorderPointTRT
					reorderQty = prod.ReorderQtyTRT
				} else {
					stockSlice = stockKGN
					reorderPt = prod.ReorderPointKGN
					reorderQty = prod.ReorderQtyKGN
				}
				stockBefore := stockSlice[skuIdx]
				qty := reorderQty
				stockSlice[skuIdx] += qty

				lineSub := qty * prod.StandardCostCents
				lineTax := finworldHSTOn(lineSub, FinWorldTaxRateStandardBps)
				lineTot := lineSub + lineTax

				pLine := FinWorldPurchaseLine{
					LineNo:            1,
					SKU:               prod.SKU,
					UOM:               prod.BaseUOM,
					Quantity:          qty,
					UnitCostCents:     prod.StandardCostCents,
					LineSubtotalCents: lineSub,
					TaxRateBps:        FinWorldTaxRateStandardBps,
					TaxCents:          lineTax,
					LineTotalCents:    lineTot,
					StockBeforeBase:   stockBefore,
					ReorderPointBase:  reorderPt,
					TriggeredReorder:  triggeredByReorder,
				}

				prEv := appendEvent(branchCode, FinWorldEventPurchaseReceipt, hour, min, 10)
				prEv.PurchaseReceipt = &FinWorldPurchaseReceiptEvent{
					PurchaseReceiptKey: prKey,
					PurchaseOrderKey:   poKey,
					BranchCode:         branchCode,
					VendorNo:           prod.VendorNo,
					TriggeredByReorder: triggeredByReorder,
					Lines:              []FinWorldPurchaseLine{pLine},
					SubtotalCents:      lineSub,
					TaxCents:           lineTax,
					TotalCents:         lineTot,
				}

				parsedIssue, _ := time.ParseInLocation("2006-01-02", td.Date, loc)
				dueDate := parsedIssue.AddDate(0, 0, 30).Format("2006-01-02")

				vbEv := appendEvent(branchCode, FinWorldEventVendorBill, hour, min, 25)
				vbEv.VendorBill = &FinWorldVendorBillEvent{
					VendorBillKey:      vbKey,
					PurchaseReceiptKey: prKey,
					PurchaseOrderKey:   poKey,
					BranchCode:         branchCode,
					VendorNo:           prod.VendorNo,
					VendorInvoiceNo:    fmt.Sprintf("VINV-%d-%05d", seed, vendorBillSeq),
					IssueDate:          td.Date,
					DueDate:            dueDate,
					Lines:              []FinWorldPurchaseLine{pLine},
					SubtotalCents:      lineSub,
					TaxCents:           lineTax,
					TotalCents:         lineTot,
				}
				monthVendorBills = append(monthVendorBills, vbEv.VendorBill)

				bs := m.Summary.ByBranch[branchCode]
				bs.PurchaseReceipts++
				if triggeredByReorder {
					bs.ReorderTriggeredReceipts++
					plan.Totals.ReorderTriggeredReceipts++
				}
				bs.VendorBills++
				m.Summary.ByBranch[branchCode] = bs
				m.Summary.PurchaseReceipts++
				m.Summary.VendorBills++
			}

			// Helper to generate one sale event for a branch and category.
			emitSale := func(branchCode string, kind FinWorldEventKind, seqInDay int, targetDailyPurchases int) {
				saleSeq++
				orderSeq++
				sKey := fmt.Sprintf("sale-s%d-%06d", seed, saleSeq)
				oKey := fmt.Sprintf("ord-s%d-%06d", seed, orderSeq)

				var cust FinWorldCustomer
				var saleMode, fulfillType, payMethod, sessionKey, invoiceKey, paymentKey, dueDate string
				var onAccount bool
				taxReason := FinWorldTaxReasonStandardON
				taxRateBps := FinWorldTaxRateStandardBps

				switch kind {
				case FinWorldEventCounterSale:
					// Counter sales are mostly homeowners (retail) and some walk-in contractors paying cash/card.
					if rng.IntN(100) < 80 {
						cust = plan.Homeowners[rng.IntN(len(plan.Homeowners))]
						if cust.TaxReliefEligible && rng.IntN(4) == 0 {
							taxReason = FinWorldTaxReasonReliefFN8
							taxRateBps = FinWorldTaxRateReliefBps
						}
					} else {
						cust = plan.Contractors[rng.IntN(len(plan.Contractors))]
					}
					saleMode = "COUNTER"
					fulfillType = "COLLECT_NOW"
					dayBranchCounterIdx[branchCode]++
					cIdx := dayBranchCounterIdx[branchCode]
					randDraw := rng.IntN(100)
					if cIdx <= 2 {
						payMethod = "CASH"
					} else if cIdx == 3 {
						payMethod = "CARD"
					} else if randDraw < 38 {
						payMethod = "CASH"
					} else {
						payMethod = "CARD"
					}
					tillIdx := (cIdx - 1) % 2
					sessionKey = sessionKeyByBranchTill[branchCode][tillIdx]
					paymentSeq++
					paymentKey = fmt.Sprintf("pay-s%d-%06d", seed, paymentSeq)

				case FinWorldEventAccountSale:
					// Pareto split: top 15 contractors get 60% of account sales.
					accountInvoiceCounter++
					if accountInvoiceCounter%10 < 6 {
						cust = plan.Contractors[rng.IntN(15)]
					} else {
						cust = plan.Contractors[15+rng.IntN(len(plan.Contractors)-15)]
					}
					saleMode = "ACCOUNT"
					fulfillType = "COLLECT_NOW"
					payMethod = "ACCOUNT"
					onAccount = true
					invoiceSeq++
					invoiceKey = fmt.Sprintf("inv-s%d-%06d", seed, invoiceSeq)

				case FinWorldEventDeliveredSale:
					accountInvoiceCounter++
					if accountInvoiceCounter%10 < 6 {
						cust = plan.Contractors[rng.IntN(15)]
					} else {
						cust = plan.Contractors[15+rng.IntN(len(plan.Contractors)-15)]
					}
					saleMode = "DELIVERED"
					fulfillType = "DELIVERED"
					payMethod = "ACCOUNT"
					onAccount = true
					invoiceSeq++
					invoiceKey = fmt.Sprintf("inv-s%d-%06d", seed, invoiceSeq)

				case FinWorldEventCollectLaterOrCODSale:
					if saleSeq%2 == 0 {
						// COLLECT_LATER on account.
						accountInvoiceCounter++
						if accountInvoiceCounter%10 < 6 {
							cust = plan.Contractors[rng.IntN(15)]
						} else {
							cust = plan.Contractors[15+rng.IntN(len(plan.Contractors)-15)]
						}
						saleMode = "COLLECT_LATER"
						fulfillType = "COLLECT_LATER"
						payMethod = "ACCOUNT"
						onAccount = true
						invoiceSeq++
						invoiceKey = fmt.Sprintf("inv-s%d-%06d", seed, invoiceSeq)
					} else {
						// Cash On Demand (COD) paid by CARD or CASH.
						cust = plan.Homeowners[rng.IntN(len(plan.Homeowners))]
						saleMode = "COD"
						fulfillType = "COD"
						if rng.IntN(2) == 0 {
							payMethod = "CARD"
						} else {
							payMethod = "CASH"
						}
						tillIdx := saleSeq % 2
						sessionKey = sessionKeyByBranchTill[branchCode][tillIdx]
						paymentSeq++
						paymentKey = fmt.Sprintf("pay-s%d-%06d", seed, paymentSeq)
					}
				}

				if onAccount {
					parsedIssue, _ := time.ParseInLocation("2006-01-02", td.Date, loc)
					dueDate = parsedIssue.AddDate(0, 0, cust.PaymentTermsDays).Format("2006-01-02")
				}

				// Build 1 to 3 lines per sale.
				numLines := 1 + rng.IntN(2)
				if kind == FinWorldEventDeliveredSale {
					numLines = 2 + rng.IntN(2)
				}
				lines := make([]FinWorldSaleLine, numLines)
				var subCents, taxCents, ontCreditCents, netTaxCents, totCents int64

				for lIdx := 0; lIdx < numLines; lIdx++ {
					prodIdx := pickWeightedProductIndex()
					for prev := 0; prev < lIdx; prev++ {
						if lines[prev].SKU == plan.Products[prodIdx].SKU {
							switch {
							case prodIdx < 5:
								prodIdx = (prodIdx + 1) % 5
							case prodIdx < 15:
								prodIdx = 5 + ((prodIdx - 5 + 1) % 10)
							default:
								prodIdx = 15 + ((prodIdx - 15 + 1) % (len(plan.Products) - 15))
							}
							prev = -1
						}
					}
					prod := plan.Products[prodIdx]

					var qty int64
					switch prod.BaseUOM {
					case "T":
						qty = int64(1 + rng.IntN(4))
					case "EA", "BAG":
						qty = int64(2 + rng.IntN(6))
					default:
						qty = int64(4 + rng.IntN(12))
					}

					unitPrice := prod.ListPriceCents
					if cust.CustomerType == "CONTRACTOR" {
						unitPrice = prod.ContractorPriceCents
					}
					if lIdx > 0 {
						for {
							candSub := qty * unitPrice
							candTax := finworldHSTOn(candSub, taxRateBps)
							var candCred int64
							if taxReason == FinWorldTaxReasonReliefFN8 {
								candCred = finworldHSTOn(candSub, FinWorldTaxOntarioCreditBps)
							}
							taxOK := taxCents+candTax == finworldHSTOn(subCents+candSub, taxRateBps)
							credOK := taxReason != FinWorldTaxReasonReliefFN8 ||
								ontCreditCents+candCred == finworldHSTOn(subCents+candSub, FinWorldTaxOntarioCreditBps)
							if taxOK && credOK {
								break
							}
							qty++
						}
					}
					lineSub := qty * unitPrice
					lineTax := finworldHSTOn(lineSub, taxRateBps)
					// The engine's ON_FN_POS_8 path collects the bound rate (the 5% net) and tracks the 8%
					// Ontario credit alongside it; the net collected equals the assessed tax
					// (core/internal/tax/service.go, assessSale).
					var lineCredit int64
					if taxReason == FinWorldTaxReasonReliefFN8 {
						lineCredit = finworldHSTOn(lineSub, FinWorldTaxOntarioCreditBps)
					}
					lineNetTax := lineTax
					lineTotal := lineSub + lineTax
					lineKey := fmt.Sprintf("%s-L%d", sKey, lIdx+1)

					lines[lIdx] = FinWorldSaleLine{
						LineNo:                 lIdx + 1,
						LineKey:                lineKey,
						SKU:                    prod.SKU,
						Description:            prod.Name,
						UOM:                    prod.SellUOM,
						Quantity:               qty,
						UnitPriceCents:         unitPrice,
						UnitCostCents:          prod.StandardCostCents,
						LineSubtotalCents:      lineSub,
						TaxReasonCode:          taxReason,
						TaxRateBps:             taxRateBps,
						TaxCents:               lineTax,
						OntarioCreditCents:     lineCredit,
						NetTaxAfterCreditCents: lineNetTax,
						LineTotalCents:         lineTotal,
					}
					subCents += lineSub
					taxCents += lineTax
					ontCreditCents += lineCredit
					netTaxCents += lineNetTax
					totCents += lineTotal

					// Deduct stock and trigger reorder if below threshold and within daily purchase budget.
					if branchCode == FinWorldBranchCodeTRT {
						stockTRT[prodIdx] -= qty
						if stockTRT[prodIdx] <= prod.ReorderPointTRT && reordersDoneToday[branchCode] < targetDailyPurchases {
							reordersDoneToday[branchCode]++
							emitPurchaseAndBill(branchCode, prodIdx, true, 9+(seqInDay%6), (seqInDay*3)%55)
						}
					} else {
						stockKGN[prodIdx] -= qty
						if stockKGN[prodIdx] <= prod.ReorderPointKGN && reordersDoneToday[branchCode] < targetDailyPurchases {
							reordersDoneToday[branchCode]++
							emitPurchaseAndBill(branchCode, prodIdx, true, 9+(seqInDay%6), (seqInDay*3)%55)
						}
					}
				}

				var paidCents, balCents int64
				if onAccount {
					balCents = totCents
				} else {
					paidCents = totCents
					if payMethod == "CASH" {
						tillCashNetBySession[sessionKey] += totCents
					} else if payMethod == "CARD" {
						dayCardSaleKeys[branchCode] = append(dayCardSaleKeys[branchCode], sKey)
						dayGrossCardCents[branchCode] += totCents
					}
				}

				hour := 8 + (seqInDay % 8)
				minute := (seqInDay * 7) % 60
				ev := appendEvent(branchCode, kind, hour, minute, 0)
				ev.Sale = &FinWorldSaleEvent{
					SaleKey:                sKey,
					OrderKey:               oKey,
					InvoiceKey:             invoiceKey,
					PaymentKey:             paymentKey,
					SessionKey:             sessionKey,
					BranchCode:             branchCode,
					CustomerKey:            cust.Key,
					CustomerType:           cust.CustomerType,
					IsTopParetoContractor:  cust.IsTopPareto,
					SaleMode:               saleMode,
					FulfillmentType:        fulfillType,
					PaymentMethod:          payMethod,
					OnAccount:              onAccount,
					IssueDate:              td.Date,
					DueDate:                dueDate,
					TaxReasonCode:          taxReason,
					TaxRateBps:             taxRateBps,
					Lines:                  lines,
					SubtotalCents:          subCents,
					TaxCents:               taxCents,
					OntarioCreditCents:     ontCreditCents,
					NetTaxAfterCreditCents: netTaxCents,
					TotalCents:             totCents,
					AmountPaidCents:        paidCents,
					InitialBalanceCents:    balCents,
				}

				// Record single-line counter cash/card sales as eligible for later POS returns.
				if saleMode == "COUNTER" && len(lines) == 1 {
					for _, ln := range lines {
						returnableByBranch[branchCode] = append(returnableByBranch[branchCode], &finworldReturnableLine{
							SaleKey:        sKey,
							InvoiceKey:     invoiceKey,
							SaleDate:       td.Date,
							CustomerKey:    cust.Key,
							PaymentMethod:  payMethod,
							TaxReasonCode:  ln.TaxReasonCode,
							TaxRateBps:     ln.TaxRateBps,
							LineNo:         ln.LineNo,
							LineKey:        ln.LineKey,
							SKU:            ln.SKU,
							UOM:            ln.UOM,
							OrigQty:        ln.Quantity,
							RemainingQty:   ln.Quantity,
							UnitPriceCents: ln.UnitPriceCents,
							UnitCostCents:  ln.UnitCostCents,
							LineTaxCents:   ln.TaxCents,
						})
					}
				}

				// If on account, register open invoice and schedule receipt (~92% overall paid, ~8% left open across aging buckets).
				if onAccount {
					payDay := -1
					// Decide whether this invoice gets paid before the end of the 12-month plan:
					// - Months 0..7 (>90 days old at anchor): 95% paid, 5% stay open (>90d bucket)
					// - Months 8..9 (31-90 days old at anchor): 90% paid, 10% stay open (31-90d bucket)
					// - Month 10 (0-30 days old at anchor): 92% paid, 8% stay open
					// - Month 11 (anchor month): 84% paid within anchor month, 16% stay open
					paidRoll := (invoiceSeq * 37) % 100
					threshold := 95
					switch {
					case mIdx <= 7:
						threshold = 95
					case mIdx <= 9:
						threshold = 90
					case mIdx == 10:
						threshold = 92
					default:
						threshold = 84
					}
					if paidRoll < threshold && globalDay+1 < totalTradingDays {
						maxLag := 14
						if globalDay+maxLag >= totalTradingDays {
							maxLag = totalTradingDays - 1 - globalDay
						}
						if maxLag < 1 {
							maxLag = 1
						}
						lag := 1 + rng.IntN(maxLag)
						payDay = globalDay + lag
					}
					openInv := &finworldOpenInvoice{
						InvoiceKey:         invoiceKey,
						SaleKey:            sKey,
						CustomerKey:        cust.Key,
						BranchCode:         branchCode,
						IssueDate:          td.Date,
						DueDate:            dueDate,
						MonthIndex:         mIdx,
						CreatedGlobalDay:   globalDay,
						ScheduledPayDay:    payDay,
						OriginalTotalCents: totCents,
						OpenBalanceCents:   totCents,
					}
					allOpenInvoices = append(allOpenInvoices, openInv)
					if payDay > globalDay {
						invoicesByPayDay[payDay] = append(invoicesByPayDay[payDay], openInv)
					}
				}

				// Update summaries.
				bs := m.Summary.ByBranch[branchCode]
				switch kind {
				case FinWorldEventCounterSale:
					bs.CounterSales++
					m.Summary.CounterSales++
				case FinWorldEventAccountSale:
					bs.AccountSales++
					m.Summary.AccountSales++
				case FinWorldEventDeliveredSale:
					bs.DeliveredSales++
					m.Summary.DeliveredSales++
				case FinWorldEventCollectLaterOrCODSale:
					bs.CollectLaterOrCODSales++
					m.Summary.CollectLaterOrCODSales++
				}
				bs.TotalSalesDocs++
				bs.TotalSalesLines += len(lines)
				bs.SalesSubtotalCents += subCents
				bs.SalesTaxCents += taxCents
				bs.SalesTotalCents += totCents
				m.Summary.ByBranch[branchCode] = bs

				m.Summary.TotalSalesDocs++
				m.Summary.TotalSalesLines += len(lines)
				m.Summary.SalesSubtotalCents += subCents
				m.Summary.SalesTaxCents += taxCents
				m.Summary.SalesTotalCents += totCents
				monthSalesLinesByBranch[branchCode] += len(lines)

				if taxReason == FinWorldTaxReasonReliefFN8 {
					plan.Totals.TaxReliefSales++
				}
				if onAccount {
					plan.Totals.TotalAccountInvoiceDocs++
					plan.Totals.AccountSalesTotalCents += totCents
					if cust.IsTopPareto {
						plan.Totals.Top15ContractorAccountSales++
						plan.Totals.Top15ContractorAccountCents += totCents
					}
				}
			}

			// Emit the day's TRT and KGN sales across all 4 sale event kinds.
			seqInDay := 0
			for i := 0; i < trtCounterPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeTRT, FinWorldEventCounterSale, seqInDay, trtPurchasePerDay[dIdx])
			}
			for i := 0; i < kgnCounterPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeKGN, FinWorldEventCounterSale, seqInDay, kgnPurchasePerDay[dIdx])
			}
			for i := 0; i < trtAccountPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeTRT, FinWorldEventAccountSale, seqInDay, trtPurchasePerDay[dIdx])
			}
			for i := 0; i < kgnAccountPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeKGN, FinWorldEventAccountSale, seqInDay, kgnPurchasePerDay[dIdx])
			}
			for i := 0; i < trtDeliveredPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeTRT, FinWorldEventDeliveredSale, seqInDay, trtPurchasePerDay[dIdx])
			}
			for i := 0; i < kgnDeliveredPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeKGN, FinWorldEventDeliveredSale, seqInDay, kgnPurchasePerDay[dIdx])
			}
			for i := 0; i < trtCollectCODPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeTRT, FinWorldEventCollectLaterOrCODSale, seqInDay, trtPurchasePerDay[dIdx])
			}
			for i := 0; i < kgnCollectCODPerDay[dIdx]; i++ {
				seqInDay++
				emitSale(FinWorldBranchCodeKGN, FinWorldEventCollectLaterOrCODSale, seqInDay, kgnPurchasePerDay[dIdx])
			}

			// 7c. Emit any remaining scheduled purchase receipts & vendor bills for today.
			for reordersDoneToday[FinWorldBranchCodeTRT] < trtPurchasePerDay[dIdx] {
				reordersDoneToday[FinWorldBranchCodeTRT]++
				// Pick the product with lowest stock relative to reorder point at TRT.
				bestIdx := 0
				bestMargin := stockTRT[0] - plan.Products[0].ReorderPointTRT
				for idx, p := range plan.Products {
					margin := stockTRT[idx] - p.ReorderPointTRT
					if margin < bestMargin {
						bestMargin = margin
						bestIdx = idx
					}
				}
				triggered := stockTRT[bestIdx] <= plan.Products[bestIdx].ReorderPointTRT
				emitPurchaseAndBill(FinWorldBranchCodeTRT, bestIdx, triggered, 13, reordersDoneToday[FinWorldBranchCodeTRT]%55)
			}
			for reordersDoneToday[FinWorldBranchCodeKGN] < kgnPurchasePerDay[dIdx] {
				reordersDoneToday[FinWorldBranchCodeKGN]++
				bestIdx := 0
				bestMargin := stockKGN[0] - plan.Products[0].ReorderPointKGN
				for idx, p := range plan.Products {
					margin := stockKGN[idx] - p.ReorderPointKGN
					if margin < bestMargin {
						bestMargin = margin
						bestIdx = idx
					}
				}
				triggered := stockKGN[bestIdx] <= plan.Products[bestIdx].ReorderPointKGN
				emitPurchaseAndBill(FinWorldBranchCodeKGN, bestIdx, triggered, 13, 20+(reordersDoneToday[FinWorldBranchCodeKGN]%35))
			}

			// 7d. Inter-branch stock transfers scheduled for today.
			for i := 0; i < transfersPerDay[dIdx]; i++ {
				transferSeq++
				prodIdx := pickWeightedProductIndex()
				prod := plan.Products[prodIdx]
				var qty int64 = 5
				if prod.BaseUOM == "T" {
					qty = 2
				}
				fromBr := FinWorldBranchCodeTRT
				toBr := FinWorldBranchCodeKGN
				if stockTRT[prodIdx] < qty*2 && stockKGN[prodIdx] >= qty*2 {
					fromBr = FinWorldBranchCodeKGN
					toBr = FinWorldBranchCodeTRT
					stockKGN[prodIdx] -= qty
					stockTRT[prodIdx] += qty
				} else {
					stockTRT[prodIdx] -= qty
					stockKGN[prodIdx] += qty
				}
				costTot := qty * prod.StandardCostCents
				ev := appendEvent(fromBr, FinWorldEventStockTransfer, 14, (i*9)%55, 0)
				ev.StockTransfer = &FinWorldStockTransferEvent{
					TransferKey:    fmt.Sprintf("xfr-s%d-%04d", seed, transferSeq),
					FromBranchCode: fromBr,
					ToBranchCode:   toBr,
					Lines: []FinWorldTransferLine{
						{
							LineNo:         1,
							SKU:            prod.SKU,
							UOM:            prod.BaseUOM,
							Quantity:       qty,
							UnitCostCents:  prod.StandardCostCents,
							LineTotalCents: costTot,
						},
					},
					TotalCostCents: costTot,
				}
				bs := m.Summary.ByBranch[fromBr]
				bs.StockTransfers++
				m.Summary.ByBranch[fromBr] = bs
				m.Summary.StockTransfers++
			}

			// 7e. Customer returns (~3% of sales lines per branch in the month).
			for _, brCode := range []string{FinWorldBranchCodeTRT, FinWorldBranchCodeKGN} {
				targetRetLines := (monthSalesLinesByBranch[brCode]*3 + 50) / 100
				pool := returnableByBranch[brCode]
				for monthReturnLinesByBranch[brCode] < targetRetLines && len(pool) > 0 {
					// Pick an eligible earlier line with RemainingQty > 0 whose refund fits today's drawer or card batch.
					pickedIdx := -1
					var retQty int64
					for k := len(pool) - 1; k >= 0; k-- {
						cand := pool[k]
						if cand.RemainingQty <= 0 {
							continue
						}
						rq := cand.OrigQty
						if cand.OrigQty > 2 && int64(math.Round(float64(cand.LineTaxCents)*2.0/float64(cand.OrigQty))) == finworldHSTOn(2*cand.UnitPriceCents, cand.TaxRateBps) {
							rq = 2
						} else if int64(math.Round(float64(cand.LineTaxCents)*1.0/float64(cand.OrigQty))) == finworldHSTOn(cand.UnitPriceCents, cand.TaxRateBps) {
							rq = 1
						}
						candSub := rq * cand.UnitPriceCents
						candTot := candSub + finworldHSTOn(candSub, cand.TaxRateBps)
						if cand.PaymentMethod == "CASH" {
							if tillCashNetBySession[sessionKeyByBranchTill[brCode][0]]-openingFloatCents < candTot+5000 {
								continue
							}
						} else {
							if dayGrossCardCents[brCode]-dayRefundCardCents[brCode] < candTot+5000 {
								continue
							}
						}
						pickedIdx = k
						retQty = rq
						break
					}
					if pickedIdx < 0 {
						break
					}
					src := pool[pickedIdx]
					src.RemainingQty = 0

					lineSub := retQty * src.UnitPriceCents
					lineTax := finworldHSTOn(lineSub, src.TaxRateBps)
					lineTot := lineSub + lineTax

					returnSeq++
					retKey := fmt.Sprintf("ret-s%d-%05d", seed, returnSeq)
					refundMethod := "CARD"
					var sKey string
					if src.PaymentMethod == "CASH" {
						refundMethod = "CASH"
						sKey = sessionKeyByBranchTill[brCode][0]
						tillCashNetBySession[sKey] -= lineTot
					} else if src.PaymentMethod == "ACCOUNT" {
						refundMethod = "ACCOUNT_CREDIT"
					} else {
						dayCardReturnKeys[brCode] = append(dayCardReturnKeys[brCode], retKey)
						dayRefundCardCents[brCode] += lineTot
					}

					ev := appendEvent(brCode, FinWorldEventReturn, 14, 30+(returnSeq%25), 0)
					ev.Return = &FinWorldReturnEvent{
						ReturnKey:          retKey,
						OriginalSaleKey:    src.SaleKey,
						OriginalInvoiceKey: src.InvoiceKey,
						OriginalSaleDate:   src.SaleDate,
						BranchCode:         brCode,
						CustomerKey:        src.CustomerKey,
						SessionKey:         sKey,
						RefundMethod:       refundMethod,
						TaxReasonCode:      src.TaxReasonCode,
						TaxRateBps:         src.TaxRateBps,
						Lines: []FinWorldReturnLine{
							{
								LineNo:            1,
								OriginalLineNo:    src.LineNo,
								OriginalLineKey:   src.LineKey,
								SKU:               src.SKU,
								UOM:               src.UOM,
								Quantity:          retQty,
								UnitPriceCents:    src.UnitPriceCents,
								UnitCostCents:     src.UnitCostCents,
								LineSubtotalCents: lineSub,
								TaxReasonCode:     src.TaxReasonCode,
								TaxRateBps:        src.TaxRateBps,
								TaxCents:          lineTax,
								LineTotalCents:    lineTot,
							},
						},
						SubtotalCents: lineSub,
						TaxCents:      lineTax,
						TotalCents:    lineTot,
					}

					monthReturnLinesByBranch[brCode]++
					bs := m.Summary.ByBranch[brCode]
					bs.ReturnDocs++
					bs.ReturnLines++
					bs.ReturnsTotalCents += lineTot
					m.Summary.ByBranch[brCode] = bs
					m.Summary.ReturnDocs++
					m.Summary.ReturnLines++
					m.Summary.ReturnsTotalCents += lineTot
				}
			}

			// 7f. Credit memos scheduled for today against earlier account invoices.
			emitCreditMemosForBranch := func(brCode string, count int) {
				for i := 0; i < count; i++ {
					var targetInv *finworldOpenInvoice
					for k := len(allOpenInvoices) - 1; k >= 0; k-- {
						cand := allOpenInvoices[k]
						if cand.BranchCode == brCode && cand.OpenBalanceCents > 15000 {
							targetInv = cand
							break
						}
					}
					if targetInv == nil {
						continue
					}
					creditMemoSeq++
					cmKey := fmt.Sprintf("cm-s%d-%04d", seed, creditMemoSeq)
					var subCents int64 = 5000 // $50.00 allowance
					taxCents := finworldHSTOn(subCents, FinWorldTaxRateStandardBps)
					totCents := subCents + taxCents
					if totCents < targetInv.OpenBalanceCents {
						targetInv.OpenBalanceCents -= totCents
					}

					ev := appendEvent(brCode, FinWorldEventCreditMemo, 15, (i*7)%50, 0)
					ev.CreditMemo = &FinWorldCreditMemoEvent{
						CreditMemoKey:      cmKey,
						OriginalSaleKey:    targetInv.SaleKey,
						OriginalInvoiceKey: targetInv.InvoiceKey,
						BranchCode:         brCode,
						CustomerKey:        targetInv.CustomerKey,
						ReasonCode:         "VOLUME_REBATE_ALLOWANCE",
						Description:        "Contractor yard handling allowance",
						TaxReasonCode:      FinWorldTaxReasonStandardON,
						TaxRateBps:         FinWorldTaxRateStandardBps,
						SubtotalCents:      subCents,
						TaxCents:           taxCents,
						TotalCents:         totCents,
					}

					bs := m.Summary.ByBranch[brCode]
					bs.CreditMemos++
					bs.CreditMemosTotalCents += totCents
					m.Summary.ByBranch[brCode] = bs
					m.Summary.CreditMemos++
					m.Summary.CreditMemosTotalCents += totCents
				}
			}
			emitCreditMemosForBranch(FinWorldBranchCodeTRT, trtCreditMemoPerDay[dIdx])
			emitCreditMemosForBranch(FinWorldBranchCodeKGN, kgnCreditMemoPerDay[dIdx])

			// 7g. Account receipts scheduled for today (paying earlier open invoices).
			dueInvoicesToday := invoicesByPayDay[globalDay]
			for _, inv := range dueInvoicesToday {
				if inv.OpenBalanceCents <= 0 {
					continue
				}
				receiptSeq++
				paymentSeq++
				rKey := fmt.Sprintf("rcpt-s%d-%06d", seed, receiptSeq)
				pKey := fmt.Sprintf("pay-s%d-%06d", seed, paymentSeq)

				payAmt := inv.OpenBalanceCents
				inv.OpenBalanceCents = 0

				method := "EFT"
				switch receiptSeq % 5 {
				case 0, 1:
					method = "CHEQUE"
				case 2, 3:
					method = "EFT"
				default:
					method = "CARD"
				}

				ev := appendEvent(inv.BranchCode, FinWorldEventAccountReceipt, 15, 20+(receiptSeq%35), 0)
				ev.AccountReceipt = &FinWorldAccountReceiptEvent{
					ReceiptKey:    rKey,
					PaymentKey:    pKey,
					BranchCode:    inv.BranchCode,
					CustomerKey:   inv.CustomerKey,
					PaymentMethod: method,
					AmountCents:   payAmt,
					Allocations: []FinWorldReceiptAllocation{
						{
							InvoiceKey:     inv.InvoiceKey,
							SaleKey:        inv.SaleKey,
							InvoiceDate:    inv.IssueDate,
							AllocatedCents: payAmt,
						},
					},
					UnappliedCents: 0,
				}

				if method == "CHEQUE" {
					dayChequeReceiptKeys[inv.BranchCode] = append(dayChequeReceiptKeys[inv.BranchCode], rKey)
					dayChequeCents[inv.BranchCode] += payAmt
					// Schedule 1 to 2 cheque receipts per month to bounce a couple of trading days later.
					if bouncesEmittedInMonth < targetBouncesInMonth && dIdx+2 < numDays {
						bouncesEmittedInMonth++
						scheduledBounces = append(scheduledBounces, &finworldScheduledBounce{
							ReceiptKey:   rKey,
							PaymentKey:   pKey,
							ReceiptDate:  td.Date,
							BranchCode:   inv.BranchCode,
							CustomerKey:  inv.CustomerKey,
							AmountCents:  payAmt,
							InvoiceKeys:  []string{inv.InvoiceKey},
							ScheduledDay: globalDay + 2,
						})
						inv.OpenBalanceCents = payAmt
						inv.Bounced = true
					}
				} else if method == "CARD" {
					dayCardReceiptKeys[inv.BranchCode] = append(dayCardReceiptKeys[inv.BranchCode], rKey)
					dayGrossCardCents[inv.BranchCode] += payAmt
				} else {
					monthEFTReceipts = append(monthEFTReceipts, ev.AccountReceipt)
				}

				bs := m.Summary.ByBranch[inv.BranchCode]
				bs.AccountReceipts++
				bs.ReceiptsTotalCents += payAmt
				m.Summary.ByBranch[inv.BranchCode] = bs
				m.Summary.AccountReceipts++
				m.Summary.ReceiptsTotalCents += payAmt
			}

			// 7h. Emit any NSF bounces scheduled for today.
			for _, sb := range scheduledBounces {
				if !sb.Emitted && sb.ScheduledDay == globalDay {
					sb.Emitted = true
					bounceSeq++
					bKey := fmt.Sprintf("nsf-s%d-%04d", seed, bounceSeq)
					ev := appendEvent(sb.BranchCode, FinWorldEventNSFBounce, 16, 5, 0)
					ev.NSFBounce = &FinWorldNSFBounceEvent{
						BounceKey:           bKey,
						OriginalReceiptKey:  sb.ReceiptKey,
						OriginalPaymentKey:  sb.PaymentKey,
						OriginalReceiptDate: sb.ReceiptDate,
						BranchCode:          sb.BranchCode,
						CustomerKey:         sb.CustomerKey,
						BouncedAmountCents:  sb.AmountCents,
						NSFFeeCents:         4500,
						ReopenedInvoiceKeys: sb.InvoiceKeys,
					}
					monthNSFBounces = append(monthNSFBounces, ev.NSFBounce)

					bs := m.Summary.ByBranch[sb.BranchCode]
					bs.NSFBounces++
					m.Summary.ByBranch[sb.BranchCode] = bs
					m.Summary.NSFBounces++
				}
			}

			// 7i. Manual journals for review (2 per month: one mid-month at TRT, one late-month at KGN).
			if dIdx == 10 || dIdx == 20 {
				journalSeq++
				brCode := FinWorldBranchCodeTRT
				if dIdx == 20 {
					brCode = FinWorldBranchCodeKGN
				}
				var jAmt int64 = 125000 // $1,250.00 yard equipment accrual
				ev := appendEvent(brCode, FinWorldEventManualJournal, 16, 30, 0)
				ev.ManualJournal = &FinWorldManualJournalEvent{
					JournalKey:     fmt.Sprintf("mj-s%d-%04d", seed, journalSeq),
					BranchCode:     brCode,
					Description:    "Monthly yard loader fuel and maintenance accrual",
					RequiresReview: true,
					ReviewStatus:   "PENDING_REVIEW",
					Lines: []FinWorldJournalLine{
						{
							LineNo:      1,
							AccountCode: "5400",
							AccountName: "Yard & Delivery Operating Expense",
							DebitCents:  jAmt,
							CreditCents: 0,
							Memo:        "Accrued yard operating expense",
						},
						{
							LineNo:      2,
							AccountCode: "2100",
							AccountName: "Accrued Liabilities",
							DebitCents:  0,
							CreditCents: jAmt,
							Memo:        "Accrued yard operating liability",
						},
					},
					TotalDebitCents:  jAmt,
					TotalCreditCents: jAmt,
				}
				bs := m.Summary.ByBranch[brCode]
				bs.ManualJournals++
				m.Summary.ByBranch[brCode] = bs
				m.Summary.ManualJournals++
			}

			// 7j. End of day: count and complete all 2 tills per branch, then daily lodgement and daily card settlement.
			varianceChoices := []int64{-250, 150, -100, 200, -175, 125}
			for bIdx, br := range plan.Branches {
				var branchLodgedCash int64
				pair := sessionKeyByBranchTill[br.Code]

				for tIdx := 0; tIdx < 2; tIdx++ {
					sKey := pair[tIdx]
					expectedCash := tillCashNetBySession[sKey]
					if expectedCash < openingFloatCents {
						expectedCash = openingFloatCents
					}
					// Roughly 1 in 10 till sessions has a small over/short variance.
					sessionOrdinal := (globalDay*4 + bIdx*2 + tIdx + int(seed%7))
					var variance int64
					hasVar := false
					if sessionOrdinal%10 == 3 {
						variance = varianceChoices[(sessionOrdinal/10)%len(varianceChoices)]
						hasVar = true
					}
					countedCash := expectedCash + variance
					lodgedCash := countedCash - openingFloatCents
					if lodgedCash < 0 {
						lodgedCash = 0
					}
					branchLodgedCash += lodgedCash

					cntEv := appendEvent(br.Code, FinWorldEventTillCount, 17, bIdx*4+tIdx*2, 0)
					cntEv.TillCount = &FinWorldTillCountEvent{
						SessionKey:        sKey,
						TillKey:           br.TillKeys[tIdx],
						BranchCode:        br.Code,
						ExpectedCashCents: expectedCash,
						CountedCashCents:  countedCash,
						VarianceCents:     variance,
						HasVariance:       hasVar,
					}

					cmpEv := appendEvent(br.Code, FinWorldEventTillComplete, 17, 10+bIdx*4+tIdx*2, 0)
					cmpEv.TillComplete = &FinWorldTillCompleteEvent{
						SessionKey:       sKey,
						TillKey:          br.TillKeys[tIdx],
						BranchCode:       br.Code,
						ClosingCashCents: countedCash,
						LodgedCashCents:  lodgedCash,
					}

					bs := m.Summary.ByBranch[br.Code]
					bs.TillCounts++
					bs.TillCompletes++
					if hasVar {
						bs.TillOverShortCount++
						m.Summary.TillOverShortCount++
					}
					m.Summary.ByBranch[br.Code] = bs
					m.Summary.TillCounts++
					m.Summary.TillCompletes++
				}

				// Daily branch lodgement (1 per branch per trading day).
				lodgementSeq++
				lKey := fmt.Sprintf("lodg-s%d-%05d", seed, lodgementSeq)
				chequeAmt := dayChequeCents[br.Code]
				totLodg := branchLodgedCash + chequeAmt
				lodgEv := appendEvent(br.Code, FinWorldEventLodgement, 17, 30+bIdx*3, 0)
				lodgEv.Lodgement = &FinWorldLodgementEvent{
					LodgementKey:      lKey,
					BranchCode:        br.Code,
					SessionKeys:       []string{pair[0], pair[1]},
					ChequeReceiptKeys: dayChequeReceiptKeys[br.Code],
					CashAmountCents:   branchLodgedCash,
					ChequeAmountCents: chequeAmt,
					TotalAmountCents:  totLodg,
					BankDepositRef:    fmt.Sprintf("DEP-%s-%05d", br.Code, lodgementSeq),
				}
				monthLodgements = append(monthLodgements, lodgEv.Lodgement)

				// Daily branch card settlement statement line (1 per branch per trading day).
				cardSettleSeq++
				csKey := fmt.Sprintf("cset-s%d-%05d", seed, cardSettleSeq)
				grossCard := dayGrossCardCents[br.Code]
				refundCard := dayRefundCardCents[br.Code]
				netBeforeFee := grossCard - refundCard
				var feeCents int64
				if netBeforeFee > 0 {
					feeCents = (netBeforeFee*150 + 5000) / 10000
				}
				netSettled := netBeforeFee - feeCents

				csEv := appendEvent(br.Code, FinWorldEventCardSettlement, 17, 45+bIdx*3, 0)
				csEv.CardSettlement = &FinWorldCardSettlementEvent{
					SettlementKey:     csKey,
					BranchCode:        br.Code,
					SaleKeys:          dayCardSaleKeys[br.Code],
					ReturnKeys:        dayCardReturnKeys[br.Code],
					ReceiptKeys:       dayCardReceiptKeys[br.Code],
					GrossCardCents:    grossCard,
					RefundCardCents:   refundCard,
					ProcessorFeeCents: feeCents,
					NetSettledCents:   netSettled,
					StatementRef:      fmt.Sprintf("CARD-%s-%05d", br.Code, cardSettleSeq),
				}
				monthCardSettlements = append(monthCardSettlements, csEv.CardSettlement)

				bs := m.Summary.ByBranch[br.Code]
				bs.Lodgements++
				bs.CardSettlements++
				m.Summary.ByBranch[br.Code] = bs
				m.Summary.Lodgements++
				m.Summary.CardSettlements++
			}

			// 7k. On the month's last trading day: import the monthly bank statement, then period close (and year close if month 12).
			if td.IsLastOfMonth {
				bankStmtSeq++
				stmtKey := fmt.Sprintf("bstmt-s%d-%03d", seed, bankStmtSeq)
				openBal := runningBankBalanceCents
				var stmtLines []FinWorldBankStatementLine
				lineNo := 0

				addStmtLine := func(valDate, kind, srcKey, ref, desc string, amt int64) {
					lineNo++
					runningBankBalanceCents += amt
					stmtLines = append(stmtLines, FinWorldBankStatementLine{
						LineNo:         lineNo,
						LineKey:        fmt.Sprintf("%s-L%04d", stmtKey, lineNo),
						ValueDate:      valDate,
						Kind:           kind,
						SourceEventKey: srcKey,
						Reference:      ref,
						Description:    desc,
						AmountCents:    amt,
					})
				}

				// Clear prior month's deposits in transit first.
				for _, pl := range priorTransitLodgements {
					addStmtLine(m.FirstTradingDay, "LODGEMENT", pl.LodgementKey, pl.BankDepositRef, "Cleared prior-month deposit in transit", pl.TotalAmountCents)
				}
				for _, pb := range priorOutstandingBills {
					addStmtLine(m.FirstTradingDay, "VENDOR_PAYMENT", pb.VendorBillKey, pb.VendorInvoiceNo, "Cleared prior-month supplier cheque", -pb.TotalCents)
				}

				// Clear all but the last 2 lodgements of this month; hold the last 2 as deposits in transit.
				transitSplit := len(monthLodgements) - 2
				if transitSplit < 0 {
					transitSplit = 0
				}
				var transitKeys []string
				for idx, l := range monthLodgements {
					if idx < transitSplit {
						addStmtLine(td.Date, "LODGEMENT", l.LodgementKey, l.BankDepositRef, "Daily branch deposit", l.TotalAmountCents)
					} else {
						transitKeys = append(transitKeys, l.LodgementKey)
					}
				}
				priorTransitLodgements = monthLodgements[transitSplit:]

				// All daily card settlements clear on the statement.
				for _, cs := range monthCardSettlements {
					addStmtLine(td.Date, "CARD_SETTLEMENT", cs.SettlementKey, cs.StatementRef, "Daily card batch settlement", cs.NetSettledCents)
				}

				// All EFT receipts clear on the statement.
				for _, eft := range monthEFTReceipts {
					addStmtLine(td.Date, "EFT_RECEIPT", eft.ReceiptKey, eft.PaymentKey, "Customer EFT receipt", eft.AmountCents)
				}

				// NSF cheque returns debit the bank account (principal on payment.bounced plus separate bank fee).
				for _, nsf := range monthNSFBounces {
					addStmtLine(td.Date, "NSF_RETURN", nsf.BounceKey, nsf.OriginalReceiptKey, "NSF returned customer cheque", -nsf.BouncedAmountCents)
					if nsf.NSFFeeCents > 0 {
						addStmtLine(td.Date, "BANK_FEE", nsf.BounceKey, "NSF-FEE-"+nsf.BounceKey, "Bank NSF returned item charge", -nsf.NSFFeeCents)
					}
				}

				// Clear all but the last 2 vendor bills; hold the last 2 as outstanding cheques.
				billSplit := len(monthVendorBills) - 2
				if billSplit < 0 {
					billSplit = 0
				}
				var outChequeKeys []string
				for idx, vb := range monthVendorBills {
					if idx < billSplit {
						addStmtLine(td.Date, "VENDOR_PAYMENT", vb.VendorBillKey, vb.VendorInvoiceNo, "Supplier cheque payment", -vb.TotalCents)
					} else {
						outChequeKeys = append(outChequeKeys, vb.VendorBillKey)
					}
				}
				priorOutstandingBills = monthVendorBills[billSplit:]

				// Monthly bank service fee.
				const bankFeeCents int64 = 3500
				addStmtLine(td.Date, "BANK_FEE", "", fmt.Sprintf("FEE-%s", m.YearMonth), "Monthly commercial account service fee", -bankFeeCents)

				bsEv := appendEvent("", FinWorldEventBankStatement, 19, 0, 0)
				bsEv.BankStatement = &FinWorldBankStatementEvent{
					StatementKey:          stmtKey,
					YearMonth:             m.YearMonth,
					StatementDate:         td.Date,
					OpeningBalanceCents:   openBal,
					ClosingBalanceCents:   runningBankBalanceCents,
					BankFeeCents:          bankFeeCents,
					Lines:                 stmtLines,
					DepositsInTransitKeys: transitKeys,
					OutstandingChequeKeys: outChequeKeys,
				}
				m.Summary.BankStatements++

				// Period close for every closed month (mIdx < 11), plus YearClose if month == 12.
				var periodCloseKey string
				if m.IsClosed {
					periodCloseSeq++
					periodCloseKey = fmt.Sprintf("pclose-s%d-%02d", seed, periodCloseSeq)
					pcEv := appendEvent("", FinWorldEventPeriodClose, 21, 0, 0)
					pcEv.PeriodClose = &FinWorldPeriodCloseEvent{
						CloseKey:       periodCloseKey,
						YearMonth:      m.YearMonth,
						FiscalYear:     m.YearMonth.Year,
						FiscalPeriod:   m.YearMonth.Month,
						LastTradingDay: td.Date,
					}
					m.Summary.PeriodCloses++
				}

				if m.IsFiscalYearEnd {
					yearCloseSeq++
					ycKey := fmt.Sprintf("yclose-s%d-%02d", seed, yearCloseSeq)
					ycEv := appendEvent("", FinWorldEventYearClose, 21, 30, 0)
					ycEv.YearClose = &FinWorldYearCloseEvent{
						YearCloseKey:   ycKey,
						FiscalYear:     m.YearMonth.Year,
						YearEndMonth:   m.YearMonth,
						PeriodCloseKey: periodCloseKey,
						LastTradingDay: td.Date,
					}
					m.Summary.YearCloses++
				}
			}
		}

		// Accumulate month summary into plan grand totals.
		plan.Totals.TotalTradingDays += m.Summary.TradingDayCount
		plan.Totals.CounterSales += m.Summary.CounterSales
		plan.Totals.AccountSales += m.Summary.AccountSales
		plan.Totals.DeliveredSales += m.Summary.DeliveredSales
		plan.Totals.CollectLaterOrCODSales += m.Summary.CollectLaterOrCODSales
		plan.Totals.TotalSalesDocs += m.Summary.TotalSalesDocs
		plan.Totals.TotalSalesLines += m.Summary.TotalSalesLines
		plan.Totals.ReturnDocs += m.Summary.ReturnDocs
		plan.Totals.ReturnLines += m.Summary.ReturnLines
		plan.Totals.CreditMemos += m.Summary.CreditMemos
		plan.Totals.AccountReceipts += m.Summary.AccountReceipts
		plan.Totals.NSFBounces += m.Summary.NSFBounces
		plan.Totals.TillOpens += m.Summary.TillOpens
		plan.Totals.TillPaidInOuts += m.Summary.TillPaidInOuts
		plan.Totals.TillCounts += m.Summary.TillCounts
		plan.Totals.TillCompletes += m.Summary.TillCompletes
		plan.Totals.TillOverShortCount += m.Summary.TillOverShortCount
		plan.Totals.Lodgements += m.Summary.Lodgements
		plan.Totals.CardSettlements += m.Summary.CardSettlements
		plan.Totals.PurchaseReceipts += m.Summary.PurchaseReceipts
		plan.Totals.VendorBills += m.Summary.VendorBills
		plan.Totals.StockTransfers += m.Summary.StockTransfers
		plan.Totals.ManualJournals += m.Summary.ManualJournals
		plan.Totals.BankStatements += m.Summary.BankStatements
		plan.Totals.PeriodCloses += m.Summary.PeriodCloses
		plan.Totals.YearCloses += m.Summary.YearCloses
		plan.Totals.SalesSubtotalCents += m.Summary.SalesSubtotalCents
		plan.Totals.SalesTaxCents += m.Summary.SalesTaxCents
		plan.Totals.SalesTotalCents += m.Summary.SalesTotalCents
		plan.Totals.ReturnsTotalCents += m.Summary.ReturnsTotalCents
		plan.Totals.CreditMemosTotalCents += m.Summary.CreditMemosTotalCents
		plan.Totals.ReceiptsTotalCents += m.Summary.ReceiptsTotalCents
	}

	plan.Totals.TotalEvents = globalSeq

	// Compute open AR aging buckets as of the anchor month's last trading day.
	anchorEndStr := plan.Months[11].LastTradingDay
	anchorEndTime, _ := time.ParseInLocation("2006-01-02", anchorEndStr, loc)
	for _, inv := range allOpenInvoices {
		if inv.OpenBalanceCents <= 0 {
			continue
		}
		issueTime, _ := time.ParseInLocation("2006-01-02", inv.IssueDate, loc)
		ageDays := int(anchorEndTime.Sub(issueTime).Hours() / 24)
		switch {
		case ageDays > 90:
			plan.Totals.OpenAROver90Count++
			plan.Totals.OpenAROver90Cents += inv.OpenBalanceCents
		case ageDays >= 31:
			plan.Totals.OpenAR31To90Count++
			plan.Totals.OpenAR31To90Cents += inv.OpenBalanceCents
		default:
			plan.Totals.OpenARCurrent0To30Cents += inv.OpenBalanceCents
		}
	}

	return plan, nil
}
