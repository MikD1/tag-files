using MediatR;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record GetLibraryCollectionRequest(
    uint Id)
    : IRequest<LibraryCollectionWithItemsDto>;