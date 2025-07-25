using MediatR;

namespace TagFilesService.Library.Contracts.LibraryCollections;

public record GetLibraryCollectionsRequest : IRequest<List<LibraryCollectionDto>>;