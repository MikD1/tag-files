using MediatR;

namespace TagFilesService.Library.Contracts;

public record AssignItemsToCollectionRequest(
    List<uint> ItemsList,
    uint CollectionId)
    : IRequest<List<LibraryItemDto>>;