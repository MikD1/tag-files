using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto;

public record CreateCategoryDto(
    string Name,
    string? TagQuery,
    FileType? ItemsType);