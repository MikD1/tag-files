using TagFilesService.Library.Contracts;

namespace TagFilesService.WebHost.Dto.LibraryCollection;

public record LibraryCollectionWithItemsDto(
    uint Id,
    string Name,
    List<LibraryItemDto> Items);