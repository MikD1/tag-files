namespace TagFilesService.WebHost.Dto.LibraryCollection;

public record LibraryCollectionDto(
    uint Id,
    string Name)
{
    public static LibraryCollectionDto FromModel(Model.LibraryCollection model) => new(
        model.Id,
        model.Name);
}