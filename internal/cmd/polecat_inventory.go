package cmd

import (
	"time"
	"fmt"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
)

const polecatSessionKeySep = "\x00"

type polecatSessionSet map[string]string

// polecatSessionActivity carries last-activity per session key, populated from a
// single tmux call. Separate from polecatSessionSet so the existing map's shape
// and every caller of it stay unchanged.
var polecatSessionActivity map[string]time.Time

// isQuiet reports whether this polecat's session has produced NOTHING for
// WorkingActivityWindow.
//
// Absence of data is NOT quiet. If activity could not be read — the bulk call
// failed, or this session was not in it — this returns false and the caller
// keeps its previous verdict. Reporting a polecat blocked because we could not
// measure it would be the same defect as gt-365 and gt-28k, where an unverifiable
// check was recorded as a definite negative and something got killed for it.
func (s polecatSessionSet) isQuiet(rigName, polecatName string) bool {
	sessionName, ok := s.lookup(rigName, polecatName)
	if !ok || polecatSessionActivity == nil {
		return false
	}
	last, ok := polecatSessionActivity[sessionName]
	if !ok || last.IsZero() {
		return false
	}
	return time.Since(last) > WorkingActivityWindow
}

type polecatInventoryItem struct {
	Rig            string
	Name           string
	State          polecat.State
	Issue          string
	CleanupStatus  string
	ActiveMR       string
	Branch         string
	SessionRunning bool
	SessionName    string
	Disposition    polecat.WorkstateDisposition
}

type polecatActiveWorkEvidence struct {
	BlocksCleanup        bool
	RequiresRestart      bool
	CountsTowardCapacity bool
	Blocker              string
	AssignedIssue        string
}

func newPolecatSessionSet(sessionNames []string) polecatSessionSet {
	sessions := make(polecatSessionSet, len(sessionNames))
	for _, sessionName := range sessionNames {
		rigName, polecatName, ok := parsePolecatSessionName(sessionName)
		if !ok {
			continue
		}
		sessions[polecatSessionKey(rigName, polecatName)] = sessionName
	}
	return sessions
}

func (s polecatSessionSet) lookup(rigName, polecatName string) (string, bool) {
	if s == nil {
		return "", false
	}
	sessionName, ok := s[polecatSessionKey(rigName, polecatName)]
	return sessionName, ok
}

func (s polecatSessionSet) namesForRig(rigName string) []string {
	if len(s) == 0 {
		return nil
	}
	var names []string
	for _, sessionName := range s {
		sessionRig, _, ok := parsePolecatSessionName(sessionName)
		if ok && sessionRig == rigName {
			names = append(names, sessionName)
		}
	}
	sort.Strings(names)
	return names
}

func polecatSessionKey(rigName, polecatName string) string {
	return rigName + polecatSessionKeySep + polecatName
}


// WorkingActivityWindow is how recently a session must have produced output for
// its polecat to count as WORKING rather than blocked.
//
// gt-fy6: the inventory decided WORKING from session EXISTENCE alone. A polecat
// frozen on a permission prompt has a live session and a hooked bead and makes no
// progress, so it was reported WORKING while holding a capacity slot — observed
// 2026-09-03 on liveop/brahmin, stuck on approval for a read-only grep and
// counted as busy for as long as it sat there.
//
// Fifteen minutes is deliberately generous. A polecat legitimately goes quiet
// while a long test suite or a build runs, and calling that blocked would be the
// same error in the other direction. This is meant to catch the agent that will
// NEVER move again without a human, not the one that is merely slow.
const WorkingActivityWindow = 15 * time.Minute

func buildPolecatInventoryItem(rigName, polecatName string, fields *beads.AgentFields, activeWork *beads.Issue, sessions polecatSessionSet) polecatInventoryItem {
	return buildPolecatInventoryItemFromEvidence(rigName, polecatName, fields, assessPolecatAssignedIssueWork(activeWork), sessions)
}

func buildPolecatInventoryItemFromEvidence(rigName, polecatName string, fields *beads.AgentFields, activeWorkEvidence polecatActiveWorkEvidence, sessions polecatSessionSet) polecatInventoryItem {
	sessionName, running := sessions.lookup(rigName, polecatName)
	item := polecatInventoryItem{
		Rig:            rigName,
		Name:           polecatName,
		State:          polecat.StateIdle,
		SessionRunning: running,
		SessionName:    sessionName,
	}

	input := polecat.WorkstateInput{State: polecat.StateIdle}
	if fields != nil {
		item.CleanupStatus = strings.TrimSpace(fields.CleanupStatus)
		item.ActiveMR = strings.TrimSpace(fields.ActiveMR)
		item.Branch = strings.TrimSpace(fields.Branch)
		switch beads.AgentState(strings.TrimSpace(fields.AgentState)) {
		case beads.AgentStateDone:
			item.State = polecat.StateDone
		}
		input.CleanupStatus = polecat.CleanupStatus(item.CleanupStatus)
		input.PushFailed = fields.PushFailed
		input.MRFailed = fields.MRFailed
		input.Branch = item.Branch
		input.ActiveMR = item.ActiveMR
	}

	if !activeWorkEvidence.BlocksCleanup && fields != nil {
		activeWorkEvidence = assessPolecatAgentStateWork(beads.AgentState(strings.TrimSpace(fields.AgentState)))
	}

	if activeWorkEvidence.BlocksCleanup {
		item.Issue = activeWorkEvidence.AssignedIssue
		if activeWorkEvidence.RequiresRestart || activeWorkEvidence.CountsTowardCapacity {
			switch {
			case !running:
				item.State = polecat.StateStalled
			case sessions.isQuiet(rigName, polecatName):
				// Session is alive with hooked work but has produced nothing for
				// WorkingActivityWindow. Report STALLED, not WORKING: an agent
				// waiting on a prompt nobody will answer is not doing work, and
				// calling it WORKING hides it from every surface that would
				// surface it. (gt-fy6)
				item.State = polecat.StateStalled
			default:
				item.State = polecat.StateWorking
			}
		} else if running && !polecat.CleanupStatus(item.CleanupStatus).IsSafe() {
			item.State = polecat.StateReviewNeeded
		}
		input.ActiveWorkBlocker = activeWorkEvidence.Blocker
		input.ActiveWorkCountsTowardCapacity = activeWorkEvidence.CountsTowardCapacity
	} else if item.State == polecat.StateIdle && running && !polecat.CleanupStatus(item.CleanupStatus).IsSafe() {
		item.State = polecat.StateReviewNeeded
	}

	if fields != nil && !activeWorkEvidence.BlocksCleanup {
		if hookBead := strings.TrimSpace(fields.HookBead); hookBead != "" {
			input.ActiveWorkBlocker = fmt.Sprintf("hook_bead=%s status=unverified", hookBead)
		}
	}
	if item.ActiveMR != "" {
		input.ActiveMRBlocker = "active_mr=" + item.ActiveMR + " status=unknown"
	}

	input.State = item.State
	item.Disposition = polecat.DecideWorkstate(input)
	return item
}

var polecatSummaryWorkStatuses = []beads.IssueStatus{
	beads.IssueStatusHooked,
	beads.StatusInProgress,
	beads.StatusOpen,
	beads.StatusBlocked,
	beads.StatusDeferred,
}

var polecatSummaryWorkStatusRank = func() map[string]int {
	ranks := make(map[string]int, len(polecatSummaryWorkStatuses))
	for i, status := range polecatSummaryWorkStatuses {
		ranks[string(status)] = i
	}
	return ranks
}()

func listActivePolecatWorkByName(bd *beads.Beads, rigName string) (map[string]*beads.Issue, error) {
	byName := make(map[string]*beads.Issue)
	issues, err := bd.ListIssueStatuses(polecatSummaryWorkStatuses...)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		evidence := assessPolecatAssignedIssueWork(issue)
		if !evidence.BlocksCleanup {
			continue
		}
		name, ok := polecatNameFromAssignee(rigName, issue.Assignee)
		if !ok {
			continue
		}
		if current := byName[name]; current == nil || polecatSummaryIssueRank(issue) < polecatSummaryIssueRank(current) {
			byName[name] = issue
		}
	}
	return byName, nil
}

func polecatSummaryIssueRank(issue *beads.Issue) int {
	if issue == nil {
		return len(polecatSummaryWorkStatuses)
	}
	if rank, ok := polecatSummaryWorkStatusRank[issue.Status]; ok {
		return rank
	}
	return len(polecatSummaryWorkStatuses)
}

func polecatNameFromAssignee(rigName, assignee string) (string, bool) {
	prefix := rigName + "/polecats/"
	if !strings.HasPrefix(assignee, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(assignee, prefix)
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func assessPolecatAssignedIssueWork(issue *beads.Issue) polecatActiveWorkEvidence {
	if issue == nil || beads.IsAgentBead(issue) || beads.IsProtectedBead(issue) || beads.IssueStatus(issue.Status).IsTerminal() {
		return polecatActiveWorkEvidence{}
	}
	requiresRestart := polecatSummaryIssueRequiresRestart(beads.IssueStatus(issue.Status))
	return polecatActiveWorkEvidence{
		BlocksCleanup:        true,
		RequiresRestart:      requiresRestart,
		CountsTowardCapacity: requiresRestart,
		Blocker:              fmt.Sprintf("assigned_work=%s status=%s", issue.ID, issue.Status),
		AssignedIssue:        issue.ID,
	}
}

func polecatSummaryIssueRequiresRestart(status beads.IssueStatus) bool {
	switch status {
	case beads.IssueStatusHooked, beads.StatusInProgress, beads.StatusOpen:
		return true
	default:
		return false
	}
}

func assessPolecatAgentStateWork(state beads.AgentState) polecatActiveWorkEvidence {
	if state == "" || state == beads.AgentStateIdle || state == beads.AgentStateDone || state == beads.AgentStateNuked {
		return polecatActiveWorkEvidence{}
	}
	if state.IsActive() {
		return polecatActiveWorkEvidence{
			BlocksCleanup:        true,
			RequiresRestart:      true,
			CountsTowardCapacity: true,
			Blocker:              fmt.Sprintf("agent_state=%s", state),
		}
	}
	if state.ProtectsFromCleanup() || state == beads.AgentStateEscalated {
		return polecatActiveWorkEvidence{
			BlocksCleanup: true,
			Blocker:       fmt.Sprintf("agent_state=%s", state),
		}
	}
	return polecatActiveWorkEvidence{}
}

func polecatActiveWorkLookupError(err error) polecatActiveWorkEvidence {
	if err == nil {
		return polecatActiveWorkEvidence{}
	}
	return polecatActiveWorkEvidence{
		BlocksCleanup: true,
		Blocker:       fmt.Sprintf("assigned_work status=lookup_error: %v", err),
	}
}

func parsePolecatAgentFields(issue *beads.Issue) *beads.AgentFields {
	if issue == nil {
		return nil
	}
	fields := beads.ParseAgentFields(issue.Description)
	fields.AgentState = beads.ResolveAgentState(issue.Description, issue.AgentState)
	return fields
}
