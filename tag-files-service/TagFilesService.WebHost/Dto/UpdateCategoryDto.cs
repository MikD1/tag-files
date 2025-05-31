using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto;

public record UpdateCategoryDto(
    string? Name,
    string? TagQuery,
    FileType? ItemsType);