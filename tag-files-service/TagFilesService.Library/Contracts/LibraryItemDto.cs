using TagFilesService.Model;

namespace TagFilesService.Library.Contracts;

public record LibraryItemDto(
    uint Id,
    string Path,
    string? ThumbnailPath,
    string? Description,
    DateTime UploadedOn,
    FileType Type,
    string MediaType,
    TimeSpan? VideoDuration,
    List<string> Tags)
{
    public static LibraryItemDto FromMetadata(FileMetadata metadata)
    {
        string? thumbnailPath = metadata.ThumbnailStatus is ThumbnailStatus.Generated
            ? System.IO.Path.Combine(Buckets.Thumbnail, FileMetadata.ChangeFileExtension(metadata.FileName, ".jpg"))
            : null;

        return new(
            metadata.Id,
            System.IO.Path.Combine(Buckets.Library, metadata.FileName),
            thumbnailPath,
            metadata.Description,
            metadata.UploadedOn,
            metadata.Type,
            metadata.MediaType,
            metadata.VideoDuration,
            metadata.Tags.Select(t => t.Name).ToList());
    }
}