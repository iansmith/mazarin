package mazdl

import _ "unsafe" // for //go:linkname

// runPluginInitTasks runs the plugin's package-init chain after the
// moduledata has been registered by the .init_array wrapper. Stock Go
// plugins go through src/plugin/plugin_dlopen.go, which calls
// runtime.plugin_lastmoduleinit (to inspect the just-registered
// moduledata and return its initTasks slice) and then runtime.doInit
// (to actually run those init funcs in dependency order). mazdl
// replicates the same two-step dance.
//
// Without this step every package-level `init()` in the plugin stays
// unrun, which leaves package-scoped state uninitialized. The
// user-visible symptom is an obscure one (sync.(*Pool) panics with
// "growslice: len out of range" the first time a fmt.Sprintf is called
// inside the plugin), because sync's init sets up the allPools slice.
//
// The go:linkname targets are the same ones stdlib's plugin package
// uses; they are carried in the host's dynsym because `runtime` is on
// the dlopen-host-packages policy, so emitHostExportsDynsym already
// marks them GLOBAL+DEFINED.
//
//go:linkname pluginLastmoduleinit plugin.lastmoduleinit
func pluginLastmoduleinit() (pluginpath string, syms map[string]any, initTasks []*initTask, errstr string)

//go:linkname runtimeDoInit runtime.doInit
func runtimeDoInit(t []*initTask)

// initTask is an opaque stand-in for runtime.initTask. Stock plugin
// package does the same trick — the loader never inspects fields, it
// only passes pointers back into runtime.doInit.
type initTask struct{}

// runPluginInitTasks is called from Open after runInitArray. It must
// be called exactly once per successful plugin load. Errors returned
// by plugin_lastmoduleinit (e.g. "plugin already loaded" or a pkghash
// mismatch) are surfaced as mazdl errors.
func runPluginInitTasks(filename string) error {
	_, _, initTasks, errstr := pluginLastmoduleinit()
	if errstr != "" {
		return errorf("Open", filename, "", "plugin_lastmoduleinit: %s", errstr)
	}
	runtimeDoInit(initTasks)
	return nil
}
