package ui

// polkitMessages are the <message> texts of packaging/polkit/
// org.archmaid.helper.policy, by action id. polkit's text agent hands the
// GUI the action id (the banner's "AUTHENTICATING FOR <id>"), so the password
// dialog looks the message up here instead of showing the bare id.
// TestPolkitMessagesMatchThePolicy ties this table to the policy file: a new
// action without an entry fails it.
var polkitMessages = map[string]string{
	"org.archmaid.helper.check-files":       "Authentication is required to check installed packages' files",
	"org.archmaid.helper.paccache":          "Authentication is required to remove old cached package versions",
	"org.archmaid.helper.paccache-wipe":     "Authentication is required to remove all cached packages",
	"org.archmaid.helper.remove-orphans":    "Authentication is required to remove orphaned packages and their unneeded dependencies",
	"org.archmaid.helper.repair":            "Authentication is required to reinstall damaged packages",
	"org.archmaid.helper.pkg-install":       "Authentication is required to install packages",
	"org.archmaid.helper.pkg-install-local": "Authentication is required to install packages built from the AUR. They are not signed, and their install scripts run as root",
	"org.archmaid.helper.pkg-remove":        "Authentication is required to remove packages",
	"org.archmaid.helper.vacuum-journal":    "Authentication is required to remove old system journal entries",
	"org.archmaid.helper.clean-tmp":         "Authentication is required to clean old temporary files",
	"org.archmaid.helper.disable-unit":      "Authentication is required to disable and stop system services",
	"org.archmaid.helper.full-cleanup":      "Authentication is required to clean the package cache, remove orphaned packages, vacuum the journal, clean temporary files and check package files",
}
