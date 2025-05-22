namespace TagFilesService.Model.Processing;

public enum ProcessingStatus
{
    Pending,
    Converting,
    AddedToLibrary,
    TemporaryFileDeleted,
    MetadataSaved,
    Done,
    Failed,
}