namespace TagFilesService.Model.Processing;

public class ProcessingFile
{
    public ProcessingFile(string fileName, uint? collectionId = null)
        : this(0u, fileName, FileType.Unknown, ProcessingStatus.WaitingForUpload, collectionId)
    {
    }

    public ProcessingFile(string fileName, FileType fileType, uint? collectionId = null)
        : this(0u, fileName, fileType, ProcessingStatus.Pending, collectionId)
    {
    }

    public uint Id { get; private set; }

    public string OriginalFileName { get; private set; }

    public string LibraryFileName { get; private set; }

    public FileType FileType { get; private set; }

    public ProcessingStatus Status { get; set; }

    public uint? CollectionId { get; private set; }

    public void SetFileType(FileType fileType)
    {
        FileType = fileType;
        LibraryFileName = GenerateLibraryFileName(fileType, OriginalFileName);
    }

    private static string GenerateLibraryFileName(FileType fileType, string originalFileName)
    {
        string fileExtension = fileType is FileType.Video ? ".mp4" : Path.GetExtension(originalFileName);
        return Guid.NewGuid().ToString().Replace("-", string.Empty) + fileExtension.ToLower();
    }

    private ProcessingFile(uint id, string originalFileName, FileType fileType, ProcessingStatus status, uint? collectionId)
    {
        Id = id;
        OriginalFileName = originalFileName;
        LibraryFileName = GenerateLibraryFileName(fileType, originalFileName);
        FileType = fileType;
        Status = status;
        CollectionId = collectionId;
    }
}
