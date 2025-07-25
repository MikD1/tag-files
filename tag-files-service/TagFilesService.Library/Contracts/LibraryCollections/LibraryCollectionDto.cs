using TagFilesService.Model;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record LibraryCollectionDto(
    uint Id,
    string Name,
    string? CoverPath)
{
    public static LibraryCollectionDto FromModel(LibraryCollection model, string? coverPath) => new(
        model.Id,
        model.Name,
        coverPath);
}