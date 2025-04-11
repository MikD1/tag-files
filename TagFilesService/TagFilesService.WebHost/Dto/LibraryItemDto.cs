using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto;

public record LibraryItemDto(
    string Path,
    string? ThumbnailPath,
    string? Description,
    DateTime UploadedOn,
    List<string> Tags)
{
    public static LibraryItemDto FromMetadata(FileMetadata metadata)
    {
        return new(
            $"library/{metadata.FileName}",
            $"thumbnail/{metadata.FileName}",
            metadata.Description,
            metadata.UploadedOn,
            metadata.Tags.Select(t => t.Name).ToList());
    }
}