using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts.LibraryCollections;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers.LibraryCollections;

public class DeleteLibraryCollectionHandler(AppDbContext dbContext) : IRequestHandler<DeleteLibraryCollectionRequest>
{
    public async Task Handle(DeleteLibraryCollectionRequest request, CancellationToken cancellationToken)
    {
        LibraryCollection? collection = await dbContext.Collections
            .FirstOrDefaultAsync(c => c.Id == request.Id, cancellationToken);
        if (collection == null)
        {
            throw new KeyNotFoundException($"Collection with ID {request.Id} not found.");
        }

        dbContext.Collections.Remove(collection);
        await dbContext.SaveChangesAsync(cancellationToken);
    }
}