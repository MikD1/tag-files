namespace TagFilesService.Library.Contracts.LibraryCollections;

public record LibraryCollectionWithItemsDto(
    uint Id,
    string Name,
    List<LibraryItemDto> Items);