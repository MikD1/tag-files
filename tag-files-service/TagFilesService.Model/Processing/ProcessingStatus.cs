namespace TagFilesService.Model.Processing;

public enum ProcessingStatus
{
    WaitingForUpload,
    Pending,
    Converting,
    AddedToLibrary,
    TemporaryFileDeleted,
    LibraryItemSaved,
    Done,
    Failed,
}