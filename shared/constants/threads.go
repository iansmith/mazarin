package constants

// ThreadPoolSize is the fixed backing array size for the thread pool.
// This determines the maximum possible thread ID (0 to ThreadPoolSize-1).
// All arrays indexed by thread ID must have this many entries.
const ThreadPoolSize = 1024
