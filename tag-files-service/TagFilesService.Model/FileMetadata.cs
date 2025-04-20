namespace TagFilesService.Model;

public class FileMetadata
{
    public FileMetadata(string fileName, FileType type, string? description)
        : this(0u, DateTime.UtcNow, fileName, type, description, ThumbnailStatus.NotGenerated, [])
    {
    }

    public uint Id { get; private set; }

    public string FileName { get; private set; }

    public DateTime UploadedOn { get; private set; }

    public FileType Type { get; private set; }

    public string? Description { get; private set; }

    public ThumbnailStatus ThumbnailStatus { get; private set; }

    public List<Tag> Tags { get; private set; }

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

    private FileMetadata(uint id, DateTime uploadedOn, string fileName, FileType type, string? description,
        ThumbnailStatus thumbnailStatus, List<Tag> tags)
    {
        ValidateDescription(description);
        Id = id;
        UploadedOn = uploadedOn;
        FileName = fileName;
        Type = type;
        Description = description;
        ThumbnailStatus = thumbnailStatus;
        Tags = tags;
    }
}