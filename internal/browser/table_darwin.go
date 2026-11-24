//go:build darwin

package browser

// table lists the supported browsers. Only Brave has been tested end to
// end; the others follow the same Chromium layout.
var table = []Browser{
	{
		Name:    "brave",
		Label:   "Brave",
		DataDir: "Library/Application Support/BraveSoftware/Brave-Browser",
		App:     "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		Inspect: "brave://inspect/#remote-debugging",
	},
	{
		Name:    "chrome",
		Label:   "Chrome",
		DataDir: "Library/Application Support/Google/Chrome",
		App:     "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "chromium",
		Label:   "Chromium",
		DataDir: "Library/Application Support/Chromium",
		App:     "/Applications/Chromium.app/Contents/MacOS/Chromium",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "edge",
		Label:   "Edge",
		DataDir: "Library/Application Support/Microsoft Edge",
		App:     "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		Inspect: "edge://inspect/#remote-debugging",
	},
	{
		Name:    "arc",
		Label:   "Arc",
		DataDir: "Library/Application Support/Arc/User Data",
		App:     "/Applications/Arc.app/Contents/MacOS/Arc",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "vivaldi",
		Label:   "Vivaldi",
		DataDir: "Library/Application Support/Vivaldi",
		App:     "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
		Inspect: "vivaldi://inspect/#remote-debugging",
	},
}
