using MediatR;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record DeleteLibraryCollectionRequest(
    uint Id)
    : IRequest;