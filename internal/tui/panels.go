package tui

// The panel and section titles that a tab's screen draws with fixed text. The
// views draw them from these constants, and the feature map
// (docs/feature-map.md, from featuremap.go) reads panelTable, so the map names
// the titles the screen shows and cannot name one the screen does not.

const (
	panelMachines        = "Machines"
	panelSelectedSession = "Selected session"
	panelGraphAllTeams   = "Graph · all teams"
	panelGraphTeam       = "Graph · team " // followed by the team's name
	panelLegend          = "Legend"
	panelPlace           = "PLACE"
	panelCharter         = "Charter"
	panelProjectRules    = "Project rules"
	panelLoop            = "Loop"
	panelRules           = "Rules"
	panelMembers         = "Members"
	panelTeamRules       = "Team rules"
	panelLocations       = "Locations"
	panelLiveSessions    = "Live sessions"
	panelEdges           = "Edges"
	panelLastChanges     = "Last changes"
	panelTeams           = "Teams"
	panelSubscriptions   = "Subscriptions"
	panelFriction        = "Friction"
	panelWorkers         = "Workers"
	panelWorkersLog      = "Workers log"
	panelDifficulty      = "Difficulty details"
	panelQualityPicker   = "Quality · worker picker"
	panelQualityScores   = "Quality · scores over time"
	panelSelectedCell    = "Selected cell"
	panelThreads         = "Threads"
	panelConversation    = "Conversation"
	panelTools           = "Tools"
	panelProjectInfo     = "Project information"
	panelSkillInfo       = "Skill information"
	panelSkillCost       = "Skill cost per project · tokens in every session"
	panelConnectedApps   = "Connected apps"
	panelIntegrations    = "Integrations"
	panelIntegrationsAll = "Integrations (all machines)"
	panelTimeZone        = "Time zone"
	panelNotifications   = "Notifications"
	panelTips            = "Tips"
	panelStanding        = "Standing orders and notes"
	panelWorkingRules    = "Working rules and guidance"
	panelNudges          = "Nudges"
	panelNeedsYou        = "Needs you"
	panelDelivery        = "Delivery"
	panelChange          = "Change"
	panelEvidence        = "Evidence"
	panelStepsEvidence   = "Steps and evidence"
	panelInboxDetail     = "Inbox detail"
	panelLog             = "Log"
)

// panelSpec is one title in panelTable.
type panelSpec struct {
	tab   int        // an index into tabs.All
	view  keyContext // the sub-view the title sits in; empty on the tab's own screen
	title string
	note  string // what the title adds when it is built at run time
}

// panelTable lists the fixed titles, tab by tab, in the order the feature map
// shows them. Dialogs and forms are not here: their keys are in the feature
// map's overlay section.
var panelTable = []panelSpec{
	{tab: sessionsTab, title: panelMachines},
	{tab: sessionsTab, title: panelSelectedSession, note: "in the expanded view"},

	{tab: graphTab, title: panelGraphAllTeams, note: "or " + panelGraphTeam + "NAME when one team is shown"},
	{tab: graphTab, title: panelLegend},
	{tab: graphTab, view: contextCard, title: panelPlace},
	{tab: graphTab, view: contextCard, title: panelCharter},
	{tab: graphTab, view: contextCard, title: panelProjectRules},
	{tab: graphTab, view: contextCard, title: panelLoop},
	{tab: graphTab, view: contextCard, title: panelRules},
	{tab: graphTab, view: contextCard, title: panelMembers},
	{tab: graphTab, view: contextCard, title: panelTeamRules},
	{tab: graphTab, view: contextCard, title: panelLocations},
	{tab: graphTab, view: contextCard, title: panelLiveSessions},
	{tab: graphTab, view: contextCard, title: panelEdges},
	{tab: graphTab, view: contextCard, title: panelLastChanges},
	{tab: graphTab, view: contextTeams, title: panelTeams},

	{tab: quotasTab, title: panelSubscriptions},
	{tab: quotasTab, title: panelFriction},
	{tab: quotasTab, title: panelWorkers, note: "with the registered and running counts"},
	{tab: quotasTab, view: contextWorkersLog, title: panelWorkersLog},
	{tab: quotasTab, view: contextWorkersLog, title: panelDifficulty},
	{tab: quotasTab, view: contextQualityDetails, title: panelQualityPicker},
	{tab: quotasTab, view: contextQualityDetails, title: panelQualityScores},
	{tab: quotasTab, view: contextQualityDetails, title: panelSelectedCell},

	{tab: messagesTab, title: panelThreads},
	{tab: messagesTab, title: panelConversation},

	{tab: toolsTab, title: panelTools, note: "with the search and the tool count"},
	{tab: toolsTab, view: contextToolsAgents, title: panelProjectInfo},
	{tab: toolsTab, view: contextToolsAgents, title: panelSkillInfo},
	{tab: toolsTab, view: contextToolsCosts, title: panelSkillCost},

	{tab: settingsTab, title: panelConnectedApps, view: contextApps},
	{tab: settingsTab, title: panelIntegrationsAll, note: "opened from the Integrations row"},
	{tab: settingsTab, title: panelMachines},
	{tab: settingsTab, title: panelTimeZone},
	{tab: settingsTab, title: panelNotifications},
	{tab: settingsTab, title: panelTips},
	{tab: settingsTab, title: panelStanding},
	{tab: settingsTab, title: panelWorkingRules},
	{tab: settingsTab, title: panelNudges},

	{tab: inboxTab, title: panelNeedsYou},
	{tab: inboxTab, title: panelDelivery},
	{tab: inboxTab, view: contextDelivery, title: panelChange},
	{tab: inboxTab, view: contextDelivery, title: panelEvidence, note: "or " + panelStepsEvidence + " for a change with steps"},
	{tab: inboxTab, view: contextInbox, title: panelInboxDetail},
	{tab: inboxTab, view: contextInbox, title: panelLog, note: "Log · SOURCE for one log source"},
}
