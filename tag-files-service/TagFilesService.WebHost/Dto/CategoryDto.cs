using TagFilesService.Model;

namespace TagFilesService.WebHost.Dto;

public record CategoryDto(
    uint Id,
    string Name,
    string? TagQuery,
    FileType? ItemsType)
{
    public static CategoryDto FromModel(Category model) => new(
        model.Id,
        model.Name,
        model.TagQuery,
        model.ItemsType);
}