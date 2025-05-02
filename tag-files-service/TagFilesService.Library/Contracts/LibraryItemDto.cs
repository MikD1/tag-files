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
    List<string> Tags)
{
    public static LibraryItemDto FromMetadata(FileMetadata metadata)
    {
        string thumbnailFileName = ChangeFileExtension(metadata.FileName, ".jpg");
        return new(
            metadata.Id,
            $"{Buckets.Library}/{metadata.FileName}",
            $"{Buckets.Thumbnail}/{thumbnailFileName}",
            metadata.Description,
            metadata.UploadedOn,
            metadata.Type,
            metadata.MediaType,
            metadata.Tags.Select(t => t.Name).ToList());
    }

    private static string ChangeFileExtension(string fileName, string newExtension)
    {
        string fileNameWithoutExtension = System.IO.Path.GetFileNameWithoutExtension(fileName);
        return fileNameWithoutExtension + newExtension;
    }
}