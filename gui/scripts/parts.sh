# The display suite's parts: one definition for headless.sh and
# headless-cover.sh, which both source this file (it sets `parts`; the
# parts_skip helper reads it). A part is "name|width|height|tags|regex": the
# screen (the wide dialogs' 1460x930 window needs 1600x1000: the screen
# bounds the window), tags "s" (built with -tags shots: the screenshot
# capture, shot.go) or "t" (the same only when ARCHMAID_GUI_SHOTS asks for
# screenshots), and the -run regex. The runners add a last part, "rest",
# that runs everything these skip, so a new test is never left out. Longest
# first (docs/TESTING.md has the times).
parts=(
	'health|1600|1000|s|^TestGUIHealth$'
	'wide|1600|1000|t|^TestGUI(WideDialogs|MinWidthToggle|GateNotesFit|RunReport)$'
	'glass|1280|800|s|^TestGUIGlass$'
	'pkgs|1280|800|t|^TestGUIPackages$'
	'minheight|1280|800|s|^TestGUIMinHeight$'
	'pkgflow|1280|800|t|^TestGUIPackages(Flow|TabsAndStatus)$'
	'align|2000|1000|s|^TestGUI(OverviewAlign|OverviewHero|LogsAlign)$'
	'perf|1280|800|t|^TestGUI(FirstFrameAndIdleRAM|Throughput)$'
	'stops|1280|800|s|^TestGUI(EngineStops|StubOwnInstance|Shots|DMSColoursWin|SmallWindow)$'
	'glass2|1280|800|s|^TestGUI(GlassShots|GlassLayout|ColourSchemeLive|SettingsScroll)$'
	'real|1280|800|t|^TestGUI(RealDriver|StopAndQuit|RunColour)$'
	'states|1280|800|s|^TestGUIMinWidthStates$'
	'narrow|1280|800|s|^TestGUI(NarrowNav|ToastPlain|MinWidth|LogsPage|SettingsCards|NavSync)$'
	'tones|1280|800|s|^TestGUI(LastRunTones|AuditRerun|AgeTick)$'
	'prompts|1280|800|t|^TestGUIPrompts$'
	'starts|1280|800|t|^TestGUI(DryFirst|ConfirmFlag|RepairNothingFound|OverviewStarts|ClearRerun)$'
	'svc|1280|800|t|^TestGUI(FullCleanup|OnePassword|RealServices)$'
	'cov-2|1280|800|t|^TestGUICov[N-Z]'
	'held|1600|1000|s|^TestGUIHeld$'
	'widelong|1600|1000|t|^TestGUIWideDialogsLong$'
	'cov-1|1280|800|t|^TestGUICov[A-M]'
	'widelongr|1600|1000|t|^TestGUIWideDialogsLongResize$'
	'healthign|1600|1000|s|^TestGUIHealthIgnore$'
)

# parts_skip prints the -skip regex of the "rest" part: every part's regex.
parts_skip() {
	local p re skip=""
	for p in "${parts[@]}"; do
		IFS='|' read -r _ _ _ _ re <<<"$p"
		skip+="${skip:+|}$re"
	done
	printf '%s' "$skip"
}
