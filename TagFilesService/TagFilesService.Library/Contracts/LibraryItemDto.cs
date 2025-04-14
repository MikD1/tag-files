using TagFilesService.Model;

namespace TagFilesService.Library.Contracts;

public record LibraryItemDto(
    uint Id,
    string Path,
    string? ThumbnailPath,
    string? Description,
    DateTime UploadedOn,
    List<string> Tags)
{
    public static LibraryItemDto FromMetadata(FileMetadata metadata)
    {
        return new(
            metadata.Id,
            $"{Buckets.Library}/{metadata.FileName}",
            $"{Buckets.Thumbnail}/{metadata.FileName}",
            metadata.Description,
            metadata.UploadedOn,
            metadata.Tags.Select(t => t.Name).ToList());
    }
}