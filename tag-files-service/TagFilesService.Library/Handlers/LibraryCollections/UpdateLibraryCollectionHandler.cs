using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts.LibraryCollections;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers.LibraryCollections;

public class UpdateLibraryCollectionHandler(AppDbContext dbContext)
    : IRequestHandler<UpdateLibraryCollectionRequest, LibraryCollectionDto>
{
    public async Task<LibraryCollectionDto> Handle(UpdateLibraryCollectionRequest request,
        CancellationToken cancellationToken)
    {
        LibraryCollection? collection = await dbContext.Collections
            .FirstOrDefaultAsync(c => c.Id == request.Id, cancellationToken);
        if (collection == null)
        {
            throw new ApplicationException($"Collection with ID {request.Id} not found.");
        }

        collection.Rename(request.Name);
        await dbContext.SaveChangesAsync(cancellationToken);
        return LibraryCollectionDto.FromModel(collection, null);
    }
}