namespace TagFilesService.WebHost.Dto;

public record LibraryItemDto(
    string Path,
    string? ThumbnailPath,
    string? Description,
    DateTime UploadedOn,
    List<string> Tags);