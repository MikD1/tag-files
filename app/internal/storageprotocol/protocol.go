// Package storageprotocol defines the small wire extension between the owned
// storage gateway and runtime S3 clients. It contains no backend/domain state.
package storageprotocol

const EpochHeader = "X-Tag-Files-Storage-Epoch"
const ExpectedEpochHeader = "X-Tag-Files-Expected-Storage-Epoch"
const EpochMismatchCode = "StorageEpochChanged"
const StorageHeader = "X-Tag-Files-Storage-ID"
const LibraryHeader = "X-Tag-Files-Library-ID"
const ExpectedStorageHeader = "X-Tag-Files-Expected-Storage-ID"
const ExpectedLibraryHeader = "X-Tag-Files-Expected-Library-ID"
const IdentityMismatchCode = "StorageIdentityChanged"
const VolumeHeader = "X-Tag-Files-Volume-ID"
const ExpectedVolumeHeader = "X-Tag-Files-Expected-Volume-ID"
