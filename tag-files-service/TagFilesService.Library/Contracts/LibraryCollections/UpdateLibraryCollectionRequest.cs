using MediatR;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record UpdateLibraryCollectionRequest(
    uint Id,
    string Name)
    : IRequest<LibraryCollectionDto>;