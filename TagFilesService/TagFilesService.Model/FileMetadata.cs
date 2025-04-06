namespace TagFilesService.Model;

public class FileMetadata
{
    public FileMetadata(string path, FileType type, string? description)
        : this(0u, DateTime.UtcNow, path, type, description, [])
    {
    }

    public uint Id { get; private set; }

    public DateTime UploadedOn { get; private set; }

    public string Path { get; private set; }

    public FileType Type { get; private set; }

    public string? Description { get; private set; }

    public List<Tag> Tags { get; private set; }

    public void UpdateDescription(string? description)
    {
        ValidateDescription(description);
        Description = description;
    }

    private void ValidateDescription(string? description)
    {
        if (description is { Length: > 150 })
        {
            throw new ApplicationException("Description cannot exceed 150 characters.");
        }
    }

    private FileMetadata(uint id, DateTime uploadedOn, string path, FileType type, string? description, List<Tag> tags)
    {
        ValidateDescription(description);
        Id = id;
        UploadedOn = uploadedOn;
        Path = path;
        Type = type;
        Description = description;
        Tags = tags;
    }
}