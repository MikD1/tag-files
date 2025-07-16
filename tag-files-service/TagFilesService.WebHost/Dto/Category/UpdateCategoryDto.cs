using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto.Category;

public record UpdateCategoryDto(
    string? Name,
    string? TagQuery,
    FileType? ItemsType);