namespace TagFilesService.Model;

public class FileMetadata
{
    public static string ChangeFileExtension(string fileName, string newExtension)
    {
        string fileNameWithoutExtension = Path.GetFileNameWithoutExtension(fileName);
        return fileNameWithoutExtension + newExtension;
    }

    public FileMetadata(string fileName, string mediaType, string? description)
        : this(0u, DateTime.UtcNow, fileName, mediaType, description, ThumbnailStatus.NotGenerated, [])
    {
    }

    public uint Id { get; private set; }

    public string FileName { get; private set; }

    public DateTime UploadedOn { get; private set; }

    public FileType Type { get; private set; }

    public string MediaType { get; private set; }

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

    private FileMetadata(uint id, DateTime uploadedOn, string fileName, string mediaType, string? description,
        ThumbnailStatus thumbnailStatus, List<Tag> tags)
    {
        ValidateDescription(description);
        Id = id;
        UploadedOn = uploadedOn;
        FileName = fileName;
        Type = GetFileType(mediaType);
        MediaType = mediaType;
        Description = description;
        ThumbnailStatus = thumbnailStatus;
        Tags = tags;
        IsFavorite = false;
    }

    private FileType GetFileType(string mediaType)
    {
        if (mediaType.StartsWith("image/"))
        {
            return FileType.Image;
        }

        if (mediaType.StartsWith("video/"))
        {
            return FileType.Video;
        }

        return FileType.Unknown;
    }
}