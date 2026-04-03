package constants

// ThreadPoolSize is the fixed backing array size for the thread pool.
// This determines the maximum possible thread ID (0 to ThreadPoolSize-1).
// All arrays indexed by thread ID must have this many entries.
const ThreadPoolSize = 1024

// MaxShepherdNameLen is the maximum length of a shepherd's TOML launch name,
// stored in proc.Shepherd.Name as a fixed-size byte array.
const MaxShepherdNameLen = 64
