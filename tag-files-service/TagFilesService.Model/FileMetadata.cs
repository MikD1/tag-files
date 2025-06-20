namespace TagFilesService.Model;

public class FileMetadata
{
    public static string ChangeFileExtension(string fileName, string newExtension)
    {
        string fileNameWithoutExtension = Path.GetFileNameWithoutExtension(fileName);
        return fileNameWithoutExtension + newExtension;
    }

    public FileMetadata(string fileName, FileType fileType, string? description)
        : this(0u, DateTime.UtcNow, fileName, fileType, description, ThumbnailStatus.NotGenerated, [])
    {
    }

    public uint Id { get; private set; }

    public string FileName { get; private set; }

    public DateTime UploadedOn { get; private set; }

    public FileType FileType { get; private set; }

    public string? Description { get; private set; }

    public TimeSpan? VideoDuration { get; set; }

    public ThumbnailStatus ThumbnailStatus { get; private set; }

    public List<Tag> Tags { get; private set; }

    public bool IsFavorite { get; private set; }

    public void ToggleFavorite()
    {
        IsFavorite = !IsFavorite;
    }

    public void UpdateDescription(string? description)
    {
        ValidateDescription(description);
        Description = description;
    }

    public void UpdateThumbnailStatus(ThumbnailStatus thumbnailStatus)
    {
        ThumbnailStatus = thumbnailStatus;
    }

    private void ValidateDescription(string? description)
    {
        if (description is { Length: > 150 })
        {
            throw new ApplicationException("Description cannot exceed 150 characters.");
        }
    }

    private FileMetadata(uint id, DateTime uploadedOn, string fileName, FileType fileType, string? description,
        ThumbnailStatus thumbnailStatus, List<Tag> tags)
    {
        ValidateDescription(description);
        Id = id;
        UploadedOn = uploadedOn;
        FileName = fileName;
        FileType = fileType;
        Description = description;
        ThumbnailStatus = thumbnailStatus;
        Tags = tags;
        IsFavorite = false;
    }
}