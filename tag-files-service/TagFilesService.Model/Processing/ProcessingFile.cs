namespace TagFilesService.Model.Processing;

public class ProcessingFile
{
    public ProcessingFile(string fileName, string contentType)
        : this(0u, fileName, contentType, ProcessingStatus.Pending)
    {
    }

    public uint Id { get; private set; }

    public string OriginalFileName { get; private set; }

    public string LibraryFileName { get; private set; }

    public string ContentType { get; private set; }

    public FileType FileType { get; private set; }

    public ProcessingStatus Status { get; set; }

    private ProcessingFile(uint id, string originalFileName, string contentType, ProcessingStatus status)
    {
        FileType fileType = GetFileType(contentType);
        string libraryFileName = GenerateLibraryFileName(fileType, originalFileName);

        Id = id;
        OriginalFileName = originalFileName;
        LibraryFileName = libraryFileName;
        ContentType = fileType is FileType.Video ? "video/mp4" : contentType;
        FileType = fileType;
        Status = status;
    }

    private FileType GetFileType(string contentType)
    {
        // TODO: Use same code as in FileMetadata
        if (contentType.StartsWith("image/"))
        {
            return FileType.Image;
        }

        if (contentType.StartsWith("video/"))
        {
            return FileType.Video;
        }

        return FileType.Unknown;
    }

    private string GenerateLibraryFileName(FileType fileType, string originalFileName)
    {
        string fileExtension = fileType is FileType.Video ? ".mp4" : Path.GetExtension(originalFileName);
        string targetFileName = Guid.NewGuid()
            .ToString()
            .Replace("-", string.Empty) + fileExtension
            .ToLower();
        return targetFileName;
    }
}