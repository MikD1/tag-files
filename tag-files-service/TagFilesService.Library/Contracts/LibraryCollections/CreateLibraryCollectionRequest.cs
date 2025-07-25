using MediatR;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record CreateLibraryCollectionRequest(
    string Name)
    : IRequest<LibraryCollectionDto>;