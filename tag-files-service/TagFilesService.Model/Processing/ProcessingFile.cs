namespace TagFilesService.Model.Processing;

public class ProcessingFile
{
    public ProcessingFile(string fileName, FileType fileType)
        : this(0u, fileName, fileType, ProcessingStatus.Pending)
    {
    }

    public uint Id { get; private set; }

    public string OriginalFileName { get; private set; }

    public string LibraryFileName { get; private set; }

    public FileType FileType { get; private set; }

    public ProcessingStatus Status { get; set; }

    private ProcessingFile(uint id, string originalFileName, FileType fileType, ProcessingStatus status)
    {
        string libraryFileName = GenerateLibraryFileName(fileType, originalFileName);

        Id = id;
        OriginalFileName = originalFileName;
        LibraryFileName = libraryFileName;
        FileType = fileType;
        Status = status;
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