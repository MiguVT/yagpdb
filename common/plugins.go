package common

var (
	Plugins []Plugin
)

type PluginCategory struct {
	Name  string
	Order int
}

var (
	PluginCategoryCore       = &PluginCategory{Name: "Core", Order: 0}
	PluginCategoryModeration = &PluginCategory{Name: "Moderation", Order: 10}
	PluginCategoryMisc       = &PluginCategory{Name: "Misc", Order: 20}
	PluginCategoryFeeds      = &PluginCategory{Name: "Feeds", Order: 30}
)

// PluginInfo represents basic plugin information
type PluginInfo struct {
	Name     string // Human readable name of the plugin
	SysName  string // snake_case version of the name in lower case
	Category *PluginCategory
}

// Plugin represents a plugin, all plugins needs to implement this at a bare minimum
type Plugin interface {
	PluginInfo() *PluginInfo
}

// RegisterPlugin registers a plugin, should be called when the bot is starting up
func RegisterPlugin(plugin Plugin) {
	Plugins = append(Plugins, plugin)
	logger.Info("Registered plugin: " + plugin.PluginInfo().Name)
}

// PluginWithCommonRun is for plugins that include a function that's always run, no matter if its the webserver frontend, bot or whatever
type PluginWithCommonRun interface {
	CommonRun()
}

// RunCommonRunPlugins runs plugins that implement PluginWithCommonRun
func RunCommonRunPlugins() {
	logger.Info("[RunCommonRunPlugins] called, Plugins list:")
	for i, v := range Plugins {
		pi := v.PluginInfo()
		logger.Infof("[RunCommonRunPlugins] Plugin #%d: %s (%T)", i, pi.Name, v)
		if cast, ok := v.(PluginWithCommonRun); ok {
		logger.Infof("[RunCommonRunPlugins] Plugin %s implements PluginWithCommonRun, calling CommonRun()", pi.Name)
			go cast.CommonRun()
		} else {
			logger.Infof("[RunCommonRunPlugins] Plugin %s does NOT implement PluginWithCommonRun", pi.Name)
		}
	}
	logger.Info("[RunCommonRunPlugins] finished loop")
}
