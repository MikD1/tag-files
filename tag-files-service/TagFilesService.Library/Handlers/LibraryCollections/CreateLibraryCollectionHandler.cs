using MediatR;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts.LibraryCollections;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers.LibraryCollections;

public class CreateLibraryCollectionHandler(AppDbContext dbContext)
    : IRequestHandler<CreateLibraryCollectionRequest, LibraryCollectionDto>
{
    public async Task<LibraryCollectionDto> Handle(CreateLibraryCollectionRequest request,
        CancellationToken cancellationToken)
    {
        LibraryCollection collection = new(request.Name);
        dbContext.Collections.Add(collection);
        await dbContext.SaveChangesAsync(cancellationToken);
        return LibraryCollectionDto.FromModel(collection, null);
    }
}