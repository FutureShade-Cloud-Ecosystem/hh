package main

// fw1_08b_part_b_branchless_post_census_test.go is S4's SOURCE census for FW1-08b part B round 3:
// every Go caller of the GL API that posts a journal draft with NO branch.
//
// WHY THIS EXISTS. The one resolver (core/internal/gl/module.go, resolveBranchID) answers the head
// office branch for a draft that names none, and that is the RIGHT answer for a company level
// journal: the consolidated books, the dealer wide write, the year end close. It is the WRONG answer
// for a document that knows its own branch: a landed cost, a vendor bill or a bank match posted to
// the head office puts a branch's money into the head office's trial balance, and nothing in the
// ledger says so.
//
// The census is the record that keeps the two apart. It walks every Go file in the core tree, tests
// included, finds every post of a journal draft, and names every one whose draft names no branch.
// A post that names no branch must be in the allowance table below with the reason it is company
// level, so a NEW branchless caller fails this census instead of landing on the head office unseen.
//
// IT IS A SOURCE SCANNER, NOT A RUNTIME ONE. The runtime half is TestFW1JournalLinesCarryBranch
// (fw1_branch_census_integration_test.go), which runs every writer over the real tables and asserts
// that no line is left with no branch. This half is the one that can say WHICH CALLER posted with no
// branch, and it says it before the branch lands in a row.
//
// WHAT IT SEES. The scanner answers what the source can SHOW: a draft built in this file and handed
// to the post in this file, including through a local variable, through a helper in the same
// package, through a helper in another package whose source is in the scanned tree, and behind a
// pointer. The draft's TYPE is resolved to its declaration, so a draft that another package aliases
// under another name is still seen. A branch field set to the EMPTY STRING names no branch, so a
// draft that spells the empty branch out is named as a branchless post too.
//
// WHAT IT CANNOT SEE. A draft that arrives as an argument (a composition root adapter, or a seam)
// is not this file's to judge: those sites are on the record in fw1_branch_census_red.go with the
// source of their branch, and the adapter's own draft names the field. A helper in another package
// is followed only when that package's source is in the scanned tree: a helper from outside the
// tree cannot be read, so the draft it returns is not named.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fw1GLPostCalls names the posting calls and the index of their journal draft argument. The index
// counts from zero: PostExternal(ctx, in) takes the draft at 1, PostExternalTx(ctx, tx, in) takes
// it at 2.
//
// The first four are the gl API's own surface. The last three are the SEAM calls a module posts
// through: their drafts are the seam packages' own types, and each one of those sites is also on
// the record in fw1_branch_census_red.go with the source of its branch.
var fw1GLPostCalls = map[string]int{
	"PostExternal":          1,
	"PostExternalTx":        2,
	"ParkExternalJournalTx": 2,
	"CreateJournal":         1,
	"PostJournal":           1,
	"PostJournalTx":         2,
	"PostCreditMemoJournal": 1,
}

// fw1DraftTypeNames are the draft types the scanner knows by NAME. A draft type is otherwise
// recognised by its fields (a branch field and a Lines field), which catches every shape whatever
// the package calls it. These names cover the one draft shape that carries no Lines field:
// billing's CreditMemoJournal, which states its three legs as roles rather than as lines.
var fw1DraftTypeNames = map[string]bool{
	"ExternalJournal":    true,
	"CreateJournalInput": true,
	"GLJournal":          true,
	"glJournal":          true,
	"CreditMemoJournal":  true,
}

// fw1BranchlessPost is one posting call whose journal draft names no branch.
type fw1BranchlessPost struct {
	file string
	line int
	fn   string
	call string
}

// key is the site's identity in the allowance table: the file, the function the post sits in and the
// call. It does not carry the line, so a line that moves does not break the table.
func (p fw1BranchlessPost) key() string {
	return p.file + "|" + p.fn + "|" + p.call
}

// fw1BranchlessAllowance is one allowance table entry: how many branchless posts the key holds, and
// why each is allowed to name no branch.
//
// The COUNT is what makes the table safe. A key alone would hide a new branchless post planted in a
// function that is already on the list; with the count, a second post in a listed function is a red
// until the table says two.
type fw1BranchlessAllowance struct {
	sites  int
	reason string
}

// fw1BranchlessPostAllowance names the posting calls that name no branch on purpose, with the reason
// each is allowed to. A key is the file, the function and the call; the count is how many branchless
// posts that key holds.
//
// Every entry here is a decision, not an excuse. The reason says WHY the head office is the right
// branch for that journal: a company level book (the consolidated books, the year end close, the
// company's own operating bank account), a leg that IS the head office proof, a fixture that writes
// no line at all, or a scaffold leg that names no document.
//
// A caller that names no branch and is NOT here is a red: either it knows its document's branch and
// must pass it, or it is company level and must say so here.
var fw1BranchlessPostAllowance = map[string]fw1BranchlessAllowance{
	"core/cmd/core/fw1_08b_branch_stamp_integration_test.go|TestFW1_08b_HeadOfficePolicyMultiBranchCloseAndCrossBranchReturn|CreateJournal":                                      {sites: 1, reason: "the leg IS the head office proof: a branchless manual journal must name no branch so it stamps the head office the Controller configured"},
	"core/cmd/core/fw1_branch_census_integration_test.go|TestFW1JournalLinesCarryBranch|CreateJournal":                                                                           {sites: 1, reason: "the leg IS the runtime census proof: a manual journal with no branch must name no branch so the resolver stamps the head office on every line it writes"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_AMonthMatchedInTheFollowingMonthCanSignOff|PostExternal":                                  {sites: 1, reason: "the fixture is the company's own operating bank account's opening entry, and bank_accounts carries no branch, so the company's books are the head office's"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_AMonthWithOneDepositInTransitAndOneOutstandingChequeCanSignOff|PostExternal":              {sites: 3, reason: "the fixtures are the company's own operating bank account's entries (a cleared deposit, a deposit in transit, a cheque in flight), and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_ChronologicalSignOffOrderAndStatementContinuity|PostExternal":                             {sites: 3, reason: "the fixtures are the company's own operating bank account's entries, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_CutoverMustReconcile|PostExternal":                                                        {sites: 3, reason: "the fixtures are the company's own operating bank account at the cut over date (the opening balance against retained earnings, a cheque in flight, a prior clearing journal), and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_CutoverPreviewRouteLegs|PostExternal":                                                     {sites: 1, reason: "the fixture is the company's own operating bank account's opening balance, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_CutoverRouteLegs|PostExternal":                                                            {sites: 2, reason: "the fixtures are the company's own operating bank account's entries, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_CutoverStartingPointAndConfirmedOutstanding|PostExternal":                                 {sites: 3, reason: "the fixtures are the company's own operating bank account's entries, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_SignOffBeforeMonthEndIsRefused|PostExternal":                                              {sites: 2, reason: "the fixtures are the company's own operating bank account's entries, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_04_month_end_integration_test.go|TestIntegration_FW1_04_SignOffRefusedWhenClearingJournalReversedAndDirectReversalRefused|PostExternal":           {sites: 1, reason: "the fixture is the company's own operating bank account's entry, and bank_accounts carries no branch"},
	"core/internal/bankrec/fw1_05_matching_integrity_integration_test.go|TestIntegration_FW1_05_CreditCardVarianceHasRightSign|PostExternal":                                     {sites: 1, reason: "the fixture is a corporate Visa purchase, and bank_accounts carries no branch, so the corporate card's own books are the company's"},
	"core/internal/bankrec/integration_test.go|TestIntegration_AC4_FeeSplitIdempotent|PostJournal":                                                                               {sites: 1, reason: "the probe posts a journal with no lines under the match's own key: it writes no journal line, so there is no line to carry a branch"},
	"core/internal/gl/approvals_rail_test.go|TestCreateJournal_ParkBridge_FiresOnlyOnManualPark|CreateJournal":                                                                   {sites: 1, reason: "the leg drives the park bridge on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/branch_reach_fence_integration_test.go|TestIntegration_FW1_10_JournalWithNoBranchIsReadByIdByADealerWideCallerOnly|CreateJournal":                          {sites: 1, reason: "a journal with no branch IS the consolidated books: the leg proves only a dealer wide caller reads it"},
	"core/internal/gl/dib418_entry_no_integration_test.go|TestIntegration_DIB418_ConcurrentPostsMintDistinctEntryNos|PostExternal":                                               {sites: 1, reason: "the fixture posts a company level journal so the leg can read the minted entry numbers"},
	"core/internal/gl/dib418_entry_no_integration_test.go|TestIntegration_DIB418_HealsAfterADirectEntryNoWrite|PostExternal":                                                     {sites: 2, reason: "the fixtures post company level journals so the leg can read the entry number heal"},
	"core/internal/gl/dib418_entry_no_integration_test.go|TestIntegration_DIB418_RolledBackMintReturnsTheNumber|PostExternal":                                                    {sites: 1, reason: "the fixture posts a company level journal so the leg can read the returned entry number"},
	"core/internal/gl/export_period_integration_test.go|seedExportJournal|PostExternal":                                                                                          {sites: 1, reason: "the fixture posts a company level journal so the period pick legs read the consolidated export the door answers"},
	"core/internal/gl/export_qbo_legs_integration_test.go|seedQBOJournalAt|PostExternal":                                                                                         {sites: 1, reason: "the fixture posts a company level journal at a chosen house day instant so the export legs read the period the day bounds"},
	"core/internal/gl/export_sage50_dates_integration_test.go|seedSageJournalAt|PostExternal":                                                                                    {sites: 1, reason: "the fixture posts a company level journal at a chosen house day instant so the export legs read the period the day bounds"},
	"core/internal/gl/fw1_01_integration_test.go|TestIntegration_FW1_01_ManualJournalIntoAClosedMonthIsStillRefused|CreateJournal":                                               {sites: 1, reason: "a manual journal with no branch is the consolidated books, and the leg proves a closed month refuses it"},
	"core/internal/gl/fw1_01_round4_integration_test.go|TestIntegration_FW1_01R4_YearEndLeavesItsOverrideRows|PostExternal":                                                      {sites: 2, reason: "the year end close consolidates across branches by definition, so the seed is the company's books"},
	"core/internal/gl/fw1_01_round5_integration_test.go|TestIntegration_FW1_01R5_FarFutureSystemJournalIsRefusedInWordsAndWritesNoRow|PostExternal":                              {sites: 1, reason: "the post is refused before it lands (a date past the books' latest year), so it writes no line at all"},
	"core/internal/gl/fw1_08b_branch_stamp_test.go|TestFW1_08b_GLWritersStampBranch|CreateJournal":                                                                               {sites: 1, reason: "the leg IS the head office stamp proof: a manual journal with no branch must name no branch"},
	"core/internal/gl/fw1_08b_part_b_external_branch_integration_test.go|TestIntegration_FW1_08b_PartB_AnExternalJournalWithNoBranchStampsTheHeadOfficeOnEveryLine|PostExternal": {sites: 1, reason: "the leg IS the external head office stamp proof: the draft must name no branch"},
	"core/internal/gl/fw1_09b_integration_test.go|fw1SeedPnL|PostExternal":                                                                                                       {sites: 2, reason: "the P&L seed is the company's consolidated books for the year end close"},
	"core/internal/gl/fw1_09b_round3_integration_test.go|TestIntegration_FW1_09bR3_JournalWritesHeldToTheCallersBranch|CreateJournal":                                            {sites: 1, reason: "a create with no branch IS the consolidated books, and the leg proves a branch scoped caller is refused it"},
	"core/internal/gl/fw1_09b_round4_integration_test.go|fw1SeedIncome|PostExternal":                                                                                             {sites: 1, reason: "the income seed is the company's consolidated books for the year end close"},
	"core/internal/gl/fw1_09c_year_end_close_integration_test.go|fw1_09cPL|PostExternal":                                                                                         {sites: 2, reason: "the P&L seed is the company's consolidated books for the year end close (FW1-09c, merged after the census)"},
	"core/internal/gl/handler_sourcerefid_live_integration_test.go|TestIntegration_ListJournals_SourceRefID_HandlerGate|PostExternal":                                            {sites: 1, reason: "the fixture posts a company level journal so the leg can read the source ref filter"},
	"core/internal/gl/integration_test.go|TestIntegration_A01_ChartOfAccountsCRUD|CreateJournal":                                                                                 {sites: 1, reason: "the fixture posts a company level journal so the leg can read the chart of accounts"},
	"core/internal/gl/integration_test.go|TestIntegration_A01_CreateJournalTrialBalanceReconciles|CreateJournal":                                                                 {sites: 1, reason: "the fixture posts a company level journal so the leg can read the trial balance"},
	"core/internal/gl/integration_test.go|TestIntegration_AC13_YearEndCloseTo3900|PostExternal":                                                                                  {sites: 2, reason: "the year end close consolidates across branches by definition, so the fixtures are the company's books"},
	"core/internal/gl/integration_test.go|TestIntegration_AC1_ClosedPeriodBlocked|CreateJournal":                                                                                 {sites: 1, reason: "a manual journal with no branch is the consolidated books, and the leg proves a closed period refuses it"},
	"core/internal/gl/integration_test.go|TestIntegration_AC2_PostExternalReplayAndBalance|PostExternal":                                                                         {sites: 2, reason: "the fixtures post company level journals so the leg can read the replay and the balance"},
	"core/internal/gl/integration_test.go|TestIntegration_AC6b_TieOutYearBoundary|PostExternal":                                                                                  {sites: 1, reason: "the fixture is the company's year end entry, and the leg ties the year boundary"},
	"core/internal/gl/integration_test.go|TestIntegration_AC9_GPParityExport|PostExternal":                                                                                       {sites: 1, reason: "the fixture posts a company level journal so the leg can read the general ledger export"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_CommitsAndRollsBackWithTheCaller|PostExternal":                                                    {sites: 1, reason: "the fixture is a company level journal: the leg is about the transaction riding the caller's, not about the branch"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_CommitsAndRollsBackWithTheCaller|PostExternalTx":                                                  {sites: 3, reason: "the fixture is a company level journal: the leg is about the transaction riding the caller's, not about the branch"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_LosesTheKeyRaceCleanly|PostExternalTx":                                                            {sites: 2, reason: "the fixture is a company level journal: the leg is about the idempotency key race, not about the branch"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_NilTxRefused|PostExternalTx":                                                                      {sites: 1, reason: "the fixture is a company level journal: the leg is about the nil transaction refusal, not about the branch"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_RefusalLeavesTheCallerUsable|PostExternalTx":                                                      {sites: 2, reason: "the fixture is a company level journal: the leg is about the refused post leaving the caller usable, not about the branch"},
	"core/internal/gl/journal_tx_integration_test.go|TestIntegration_JournalTx_TheFailurePathLeavesNoOpenTransaction|PostExternalTx":                                             {sites: 1, reason: "the fixture is a company level journal: the leg is about the failure path closing the transaction, not about the branch"},
	"core/internal/gl/list_journals_source_ref_integration_test.go|TestIntegration_ListJournals_FilterBySourceRefID|PostExternal":                                                {sites: 1, reason: "the fixture posts a company level journal so the leg can read the source ref filter"},
	"core/internal/gl/post_external_guard_test.go|TestPostExternal_RejectsNegativeLeg|PostExternal":                                                                              {sites: 3, reason: "the leg drives the negative leg guard on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/posting_window_integration_test.go|TestIntegration_GL_PostingDateRefusal_MatchesThePostingPath|PostExternal":                                               {sites: 2, reason: "the fixtures post company level journals so the leg can read the posting window against the posting path it mirrors"},
	"core/internal/gl/reversal_guard_integration_test.go|TestIntegration_FW1_09a_AJournalCanBeReversedOnce|CreateJournal":                                                        {sites: 1, reason: "the fixture posts a company level journal so the leg can read the reversal guard"},
	"core/internal/gl/reversal_guard_integration_test.go|TestIntegration_FW1_09a_ASystemJournalIsNotReversedFromTheLedger|PostExternal":                                          {sites: 1, reason: "the fixture posts a company level system journal so the leg can read the reversal guard"},
	"core/internal/gl/reversal_guard_integration_test.go|TestIntegration_FW1_09a_AnAccountAPostingRuleNeedsCannotBeDeactivated|CreateJournal":                                    {sites: 1, reason: "the fixture posts a company level journal so the leg can read the posting rule guard"},
	"core/internal/gl/reversal_guard_integration_test.go|TestIntegration_FW1_09a_TheTableRefusesASecondReversalWhoeverWritesIt|CreateJournal":                                    {sites: 1, reason: "the fixture posts a company level journal so the leg can read the reversal guard"},
	"core/internal/gl/reversal_guard_integration_test.go|TestIntegration_FW1_09a_TwoConcurrentReversalsOfOneJournalLandOnlyOne|CreateJournal":                                    {sites: 1, reason: "the fixture posts a company level journal so the leg can read the reversal guard"},
	"core/internal/gl/service_test.go|TestAC11_ReversingJournalNetZero|CreateJournal":                                                                                            {sites: 1, reason: "the leg drives the reversal on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/service_test.go|TestAC1_PostJournalInvariants|CreateJournal":                                                                                               {sites: 3, reason: "the leg drives the journal invariants on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/service_test.go|TestAC4_ApproveRefusesEveryStateButApproved|CreateJournal":                                                                                 {sites: 1, reason: "the leg drives the approval rail on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/service_test.go|TestAC4_MakerChecker|CreateJournal":                                                                                                        {sites: 1, reason: "the leg drives the maker checker on the offline scaffold and names no document, so the head office is the company's book"},
	"core/internal/gl/zone_integration_test.go|TestIntegration_JournalPeriodFollowsTheDealersZone|PostExternal":                                                                  {sites: 1, reason: "the fixture posts a company level journal so the leg can read the period the dealer's zone names"},
	"core/internal/gl/zone_integration_test.go|TestIntegration_JournalRefusedWhenTheZoneCannotBeRead|PostExternal":                                                               {sites: 1, reason: "the fixture posts a company level journal so the leg can read the zone refusal"},
}

// fw1CensusResult is the census's answer about one tree: every branchless post it found, grouped by
// the allowance key, and every allowance entry that no longer names a live site.
type fw1CensusResult struct {
	found map[string][]fw1BranchlessPost
	stale []string
}

// fw1RunBranchlessCensus scans a tree and answers what the census found there. It does not judge:
// the judgement is the caller's, so the same scan can run against the real tree and against the
// probe tree the red leg plants a branchless caller in.
func fw1RunBranchlessCensus(coreRoot string) (fw1CensusResult, error) {
	var result fw1CensusResult
	posts, err := fw1FindBranchlessPosts(coreRoot)
	if err != nil {
		return result, err
	}
	result.found = map[string][]fw1BranchlessPost{}
	for _, p := range posts {
		result.found[p.key()] = append(result.found[p.key()], p)
	}
	for key := range fw1BranchlessPostAllowance {
		if _, live := result.found[key]; !live {
			result.stale = append(result.stale, key)
		}
	}
	sort.Strings(result.stale)
	return result, nil
}

// TestFW1_08b_PartB_Round3_NoGLCallerPostsWithNoBranch is the census. It fails on every posting
// call whose draft names no branch and that is not in the allowance table, and it fails on every
// allowance entry that no longer names a live site (a stale entry would hide a new one).
func TestFW1_08b_PartB_Round3_NoGLCallerPostsWithNoBranch(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	result, err := fw1RunBranchlessCensus(coreRoot)
	if err != nil {
		t.Fatalf("scanning %s: %v", coreRoot, err)
	}
	for key, group := range result.found {
		allowance, allowed := fw1BranchlessPostAllowance[key]
		if !allowed {
			for _, p := range group {
				t.Errorf("gl API post with no branch: %s:%d: %s: %s: the draft names no branch, so the resolver stamps the head office",
					p.file, p.line, p.fn, p.call)
			}
			continue
		}
		if len(group) != allowance.sites {
			t.Errorf("branchless post count changed at %s: %d site(s) now, the allowance says %d: a new branchless caller must say why it stays company level", key, len(group), allowance.sites)
			continue
		}
		for _, p := range group {
			t.Logf("allowed branchless post %s:%d: %s: %s", p.file, p.line, p.fn, allowance.reason)
		}
	}
	for _, key := range result.stale {
		t.Errorf("stale allowance entry %s: no such branchless post any more (the reason was: %s)", key, fw1BranchlessPostAllowance[key].reason)
	}
}

// fw1SourceType is one struct type: its field names, and the type it aliases when it is an alias.
type fw1SourceType struct {
	fields []string
	alias  ast.Expr
	file   *fw1SourceFile
}

// fw1SourceFunc is a package level function and the file it sits in.
type fw1SourceFunc struct {
	decl *ast.FuncDecl
	file *fw1SourceFile
}

// fw1SourceFile is one parsed Go file.
type fw1SourceFile struct {
	rel     string
	dir     string
	ast     *ast.File
	fset    *token.FileSet
	imports map[string]string
}

// fw1SourcePkg is one package: its files, its package level functions and its struct types.
type fw1SourcePkg struct {
	dir   string
	name  string
	files []*fw1SourceFile
	funcs map[string]*fw1SourceFunc
	types map[string]*fw1SourceType
}

// fw1SourceTree is every package under the core tree, keyed by its directory and package name.
type fw1SourceTree struct {
	coreRoot string
	module   string
	pkgs     map[string]*fw1SourcePkg
}

// fw1FindBranchlessPosts walks the core tree and answers every posting call whose journal draft
// names no branch.
func fw1FindBranchlessPosts(coreRoot string) ([]fw1BranchlessPost, error) {
	tree, err := fw1ReadSourceTree(coreRoot)
	if err != nil {
		return nil, err
	}
	var posts []fw1BranchlessPost
	for _, key := range tree.sortedPkgKeys() {
		pkg := tree.pkgs[key]
		for _, f := range pkg.files {
			posts = append(posts, fw1BranchlessPostsInFile(tree, pkg, f)...)
		}
	}
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].file != posts[j].file {
			return posts[i].file < posts[j].file
		}
		return posts[i].line < posts[j].line
	})
	return posts, nil
}

// fw1ReadSourceTree parses every Go file under the core tree, tests included, and groups the files
// by package.
func fw1ReadSourceTree(coreRoot string) (*fw1SourceTree, error) {
	module, err := fw1ReadModulePath(coreRoot)
	if err != nil {
		return nil, err
	}
	tree := &fw1SourceTree{coreRoot: coreRoot, module: module, pkgs: map[string]*fw1SourcePkg{}}
	fset := token.NewFileSet()
	for _, sub := range []string{"cmd", "internal"} {
		root := filepath.Join(coreRoot, sub)
		if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			relFromCore, rerr := filepath.Rel(coreRoot, path)
			if rerr != nil {
				return rerr
			}
			rel := filepath.ToSlash(filepath.Join("core", relFromCore))
			fileAST, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", rel, perr)
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			key := dir + "|" + fileAST.Name.Name
			pkg := tree.pkgs[key]
			if pkg == nil {
				pkg = &fw1SourcePkg{
					dir:   dir,
					name:  fileAST.Name.Name,
					funcs: map[string]*fw1SourceFunc{},
					types: map[string]*fw1SourceType{},
				}
				tree.pkgs[key] = pkg
			}
			f := &fw1SourceFile{rel: rel, dir: dir, ast: fileAST, fset: fset, imports: fw1FileImports(fileAST)}
			pkg.files = append(pkg.files, f)
			for _, decl := range fileAST.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv != nil {
						continue
					}
					if _, taken := pkg.funcs[d.Name.Name]; taken {
						continue
					}
					pkg.funcs[d.Name.Name] = &fw1SourceFunc{decl: d, file: f}
				case *ast.GenDecl:
					if d.Tok != token.TYPE {
						continue
					}
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						if _, taken := pkg.types[ts.Name.Name]; taken {
							continue
						}
						pkg.types[ts.Name.Name] = fw1SourceTypeOf(ts, f)
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", root, err)
		}
	}
	for _, pkg := range tree.pkgs {
		sort.Slice(pkg.files, func(i, j int) bool { return pkg.files[i].rel < pkg.files[j].rel })
	}
	return tree, nil
}

// fw1SourceTypeOf answers the index entry for one type declaration.
func fw1SourceTypeOf(ts *ast.TypeSpec, file *fw1SourceFile) *fw1SourceType {
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		// an alias (type X = Y) or a named non struct type
		return &fw1SourceType{alias: ts.Type, file: file}
	}
	entry := &fw1SourceType{file: file}
	if st.Fields != nil {
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				entry.fields = append(entry.fields, name.Name)
			}
		}
	}
	return entry
}

// fw1FileImports answers the import qualifier of a file: the alias when the import names one, the
// package's own last path element otherwise.
func fw1FileImports(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		qualifier := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			qualifier = path[idx+1:]
		}
		if imp.Name != nil {
			qualifier = imp.Name.Name
		}
		imports[qualifier] = path
	}
	return imports
}

// fw1ReadModulePath answers the module path the tree's packages import each other by.
func fw1ReadModulePath(coreRoot string) (string, error) {
	body, err := os.ReadFile(filepath.Join(coreRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("go.mod names no module path")
}

// fw1SortedPkgKeys answers every package's key, in a stable order.
func (tree *fw1SourceTree) sortedPkgKeys() []string {
	keys := make([]string, 0, len(tree.pkgs))
	for k := range tree.pkgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fw1PkgForImport answers the package an import path names, when it names one in this tree.
func (tree *fw1SourceTree) fw1PkgForImport(path string) *fw1SourcePkg {
	prefix := tree.module + "/"
	if !strings.HasPrefix(path, prefix) {
		return nil
	}
	dir := filepath.ToSlash(filepath.Join("core", strings.TrimPrefix(path, prefix)))
	name := path[strings.LastIndex(path, "/")+1:]
	return tree.pkgs[dir+"|"+name]
}

// fw1BranchlessPostsInFile answers every posting call in one file whose journal draft names no
// branch.
func fw1BranchlessPostsInFile(tree *fw1SourceTree, pkg *fw1SourcePkg, file *fw1SourceFile) []fw1BranchlessPost {
	var posts []fw1BranchlessPost
	for _, decl := range file.ast.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := fw1CallName(call)
			idx, isPost := fw1GLPostCalls[name]
			if !isPost || len(call.Args) <= idx {
				return true
			}
			drafts := fw1ResolveDrafts(tree, pkg, file, fn, call.Args[idx], 0)
			if len(drafts) == 0 {
				return true
			}
			if !fw1DraftsProveBranchless(tree, pkg, file, fn, drafts) {
				return true
			}
			posts = append(posts, fw1BranchlessPost{
				file: file.rel,
				line: file.fset.Position(call.Pos()).Line,
				fn:   fw1FormatFuncName(fn),
				call: name,
			})
			return true
		})
	}
	return posts
}

// fw1CallName answers the name of the function a call reaches: the selector's name for a method
// call, the bare name for a plain call.
func fw1CallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

// fw1IsBranchField answers whether a struct field name is the branch.
func fw1IsBranchField(name string) bool {
	return name == "branch" || strings.EqualFold(name, "branchid")
}

// fw1HasField answers whether a field list holds a field with the given name.
func fw1HasField(fields []string, name string) bool {
	for _, f := range fields {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

// fw1IsDraftType answers whether a type is a journal draft: the type carries a branch field, and
// either carries a Lines field (the journal shapes) or is one of the names the scanner knows.
func fw1IsDraftType(fields []string, name string) bool {
	if !fw1HasField(fields, "branchid") && !fw1HasField(fields, "branch") {
		return false
	}
	if fw1DraftTypeNames[name] {
		return true
	}
	return fw1HasField(fields, "lines")
}

// fw1DraftBranchState answers whether a draft literal names a branch, and whether the file can show
// that at all. A positional literal takes the type's own field order, so the scanner cannot say
// which field is the branch: it answers "cannot show", and the site is left alone rather than
// called a branchless post on a guess.
//
// A branch field set to the EMPTY STRING names no branch. The resolver sends an empty branch to
// the head office exactly as it sends a draft that leaves the field out, so a draft that spells the
// empty branch out is a branchless post and is named as one.
func fw1DraftBranchState(lit *ast.CompositeLit) (named bool, known bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return false, false
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if fw1IsBranchField(key.Name) {
			if fw1IsEmptyStringLiteral(kv.Value) {
				return false, true
			}
			return true, true
		}
	}
	return false, true
}

// fw1IsEmptyStringLiteral answers whether an expression is the empty string literal, in either
// quoting. A branch field holding it names no branch.
func fw1IsEmptyStringLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(lit.Value)
	return err == nil && value == ""
}

// fw1DraftsProveBranchless answers whether every draft a posting call can hand over names no
// branch. A helper that returns an empty draft on its error path and a stamped draft on its good
// path is not a branchless caller: the stamped path is the one that reaches the ledger.
func fw1DraftsProveBranchless(tree *fw1SourceTree, pkg *fw1SourcePkg, file *fw1SourceFile, fn *ast.FuncDecl, drafts []*ast.CompositeLit) bool {
	proven := false
	for _, lit := range drafts {
		if !fw1IsDraftLiteral(tree, pkg, file, lit) {
			continue
		}
		named, known := fw1DraftBranchState(lit)
		if !known {
			continue
		}
		if named {
			return false
		}
		proven = true
	}
	return proven
}

// fw1IsDraftLiteral answers whether a composite literal builds a journal draft, by resolving the
// literal's type to its declaration and reading the fields.
func fw1IsDraftLiteral(tree *fw1SourceTree, pkg *fw1SourcePkg, file *fw1SourceFile, lit *ast.CompositeLit) bool {
	fields, name, ok := fw1ResolveTypeFields(tree, pkg, file, lit.Type, 0)
	if !ok {
		return false
	}
	return fw1IsDraftType(fields, name)
}

// fw1ResolveTypeFields answers the field names and the type name of the type an expression names,
// following an alias to the type it points at.
func fw1ResolveTypeFields(tree *fw1SourceTree, pkg *fw1SourcePkg, file *fw1SourceFile, expr ast.Expr, depth int) ([]string, string, bool) {
	if depth > 8 {
		return nil, "", false
	}
	var target *fw1SourcePkg
	var name string
	switch t := expr.(type) {
	case *ast.Ident:
		target = pkg
		name = t.Name
	case *ast.SelectorExpr:
		qualifier, ok := t.X.(*ast.Ident)
		if !ok {
			return nil, "", false
		}
		path, ok := file.imports[qualifier.Name]
		if !ok {
			return nil, "", false
		}
		target = tree.fw1PkgForImport(path)
		name = t.Sel.Name
	default:
		return nil, "", false
	}
	if target == nil {
		return nil, "", false
	}
	entry, ok := target.types[name]
	if !ok {
		return nil, "", false
	}
	if entry.alias != nil {
		return fw1ResolveTypeFields(tree, target, entry.file, entry.alias, depth+1)
	}
	return entry.fields, name, true
}

// fw1ResolveDrafts answers the composite literals a posting call hands over, when the call's own
// package can show them: the literal itself, a local variable that holds it, or the results of a
// helper in the same package. A helper in ANOTHER package is followed too, into that package's own
// source, when that package is in the scanned tree.
//
// It answers nothing when the draft comes from somewhere the package cannot show: an argument the
// caller passes in, or a helper in a package outside the scanned tree. Those sites are the seam
// table's record in fw1_branch_census_red.go, not this scanner's.
//
// A helper answers EVERY draft it returns, not just its first return: a builder's error return is
// usually an empty draft, and calling that a branchless post would be a false alarm.
func fw1ResolveDrafts(tree *fw1SourceTree, pkg *fw1SourcePkg, file *fw1SourceFile, fn *ast.FuncDecl, expr ast.Expr, depth int) []*ast.CompositeLit {
	if depth > 6 || expr == nil {
		return nil
	}
	switch v := expr.(type) {
	case *ast.CompositeLit:
		return []*ast.CompositeLit{v}
	case *ast.ParenExpr:
		return fw1ResolveDrafts(tree, pkg, file, fn, v.X, depth+1)
	case *ast.UnaryExpr:
		// a pointer to a draft: &gl.ExternalJournal{...}
		if v.Op != token.AND {
			return nil
		}
		return fw1ResolveDrafts(tree, pkg, file, fn, v.X, depth+1)
	case *ast.StarExpr:
		// the draft behind a pointer: *draft
		return fw1ResolveDrafts(tree, pkg, file, fn, v.X, depth+1)
	case *ast.Ident:
		if rhs := fw1LocalDraft(fn, v.Name); rhs != nil {
			return fw1ResolveDrafts(tree, pkg, file, fn, rhs, depth+1)
		}
		if helper, ok := pkg.funcs[v.Name]; ok {
			return fw1FuncResultDrafts(tree, pkg, helper, depth+1)
		}
		return nil
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok {
			if helper, ok := pkg.funcs[id.Name]; ok {
				return fw1FuncResultDrafts(tree, pkg, helper, depth+1)
			}
			return nil
		}
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
			return fw1ResolveOtherPackageDrafts(tree, file, sel, depth)
		}
		return nil
	}
	return nil
}

// fw1ResolveOtherPackageDrafts answers the drafts a helper in ANOTHER package returns. The scanner
// reads that package's source too, so the helper is followed into its own file, where its own
// imports resolve the types it names.
//
// The helper's package must be in the scanned tree. A helper from outside the tree cannot be read,
// so the draft it returns is not named: that is the seam table's record in fw1_branch_census_red.go,
// not this scanner's.
func fw1ResolveOtherPackageDrafts(tree *fw1SourceTree, file *fw1SourceFile, sel *ast.SelectorExpr, depth int) []*ast.CompositeLit {
	if depth > 6 {
		return nil
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	path, ok := file.imports[qualifier.Name]
	if !ok {
		return nil
	}
	target := tree.fw1PkgForImport(path)
	if target == nil {
		return nil
	}
	helper, ok := target.funcs[sel.Sel.Name]
	if !ok {
		return nil
	}
	return fw1FuncResultDrafts(tree, target, helper, depth+1)
}

// fw1LocalDraft answers the expression a local variable of the given function was given, when the
// function shows it.
func fw1LocalDraft(fn *ast.FuncDecl, name string) ast.Expr {
	if fn == nil || fn.Body == nil {
		return nil
	}
	var found ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name || i >= len(s.Rhs) {
					continue
				}
				found = s.Rhs[i]
			}
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range vs.Names {
					if n.Name != name || i >= len(vs.Values) {
						continue
					}
					found = vs.Values[i]
				}
			}
		}
		return true
	})
	return found
}

// fw1FuncResultDrafts answers every draft a helper in the package returns.
func fw1FuncResultDrafts(tree *fw1SourceTree, pkg *fw1SourcePkg, helper *fw1SourceFunc, depth int) []*ast.CompositeLit {
	if helper == nil || helper.decl == nil || helper.decl.Body == nil {
		return nil
	}
	var results []ast.Expr
	ast.Inspect(helper.decl.Body, func(n ast.Node) bool {
		if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) > 0 {
			results = append(results, r.Results[0])
		}
		return true
	})
	var drafts []*ast.CompositeLit
	for _, res := range results {
		drafts = append(drafts, fw1ResolveDrafts(tree, pkg, helper.file, helper.decl, res, depth)...)
	}
	return drafts
}
