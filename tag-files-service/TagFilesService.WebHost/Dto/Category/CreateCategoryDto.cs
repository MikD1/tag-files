using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto.Category;

public record CreateCategoryDto(
    string Name,
    string? TagQuery,
    FileType? ItemsType);