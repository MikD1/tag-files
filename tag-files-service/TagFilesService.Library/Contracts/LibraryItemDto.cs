using TagFilesService.Model;

namespace TagFilesService.Library.Contracts;

public record LibraryItemDto(
    uint Id,
    string Path,
    string? ThumbnailPath,
    string? Description,
    DateTime UploadedOn,
    FileType FileType,
    TimeSpan? VideoDuration,
    List<string> Tags,
    bool IsFavorite)
{
    public static LibraryItemDto FromModel(LibraryItem model)
    {
        string? thumbnailPath = model.ThumbnailStatus is ThumbnailStatus.Generated
            ? System.IO.Path.Combine(Buckets.Thumbnail, LibraryItem.ChangeFileExtension(model.FileName, ".jpg"))
            : null;

        return new(
            model.Id,
            System.IO.Path.Combine(Buckets.Library, model.FileName),
            thumbnailPath,
            model.Description,
            model.UploadedOn,
            model.FileType,
            model.VideoDuration,
            model.Tags
                .OrderBy(t => t.Name)
                .Select(t => t.Name)
                .ToList(),
            model.IsFavorite);
    }
}