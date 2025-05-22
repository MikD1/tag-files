namespace TagFilesService.Model.Processing;

public enum ProcessingStatus
{
    New,
    Converting,
    AddedToLibrary,
    TemporaryFileDeleted,
    MetadataSaved,
    Done,
    Failed,
}