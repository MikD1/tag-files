// Package storageprotocol defines the small wire extension between the owned
// storage gateway and runtime S3 clients. It contains no backend/domain state.
package storageprotocol

const EpochHeader = "X-Tag-Files-Storage-Epoch"
const ExpectedEpochHeader = "X-Tag-Files-Expected-Storage-Epoch"
const EpochMismatchCode = "StorageEpochChanged"
